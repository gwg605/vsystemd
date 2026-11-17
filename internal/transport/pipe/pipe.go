//go:build windows

package pipe

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/transport"
)

// errnoFileNotFound is ERROR_FILE_NOT_FOUND and errnoPipeBusy is ERROR_PIPE_BUSY.
const (
	errnoFileNotFound = syscall.Errno(2)
	errnoPipeBusy     = syscall.Errno(231)

	daemonPipeACL = "D:(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;OW)(A;;GA;;;S-1-5-80-0)"
)

// daemonPipeName is a variable so tests can avoid clashing with a running daemon.
var daemonPipeName = `\\.\pipe\vsystemd-in`

type envelope struct {
	From string            `json:"from"`
	Msg  transport.Message `json:"msg"`
}

// Transport is a named-pipe transport in a client-server topology: the daemon
// owns the single inbound pipe and accepts connections, while runners and
// control clients connect to it. A connection is duplex, so replies travel back
// on the connection a client opened.
type Transport struct {
	local    transport.Peer
	config   transport.Config
	core     *transport.Core
	auth     authenticator
	server   bool
	listener *pipeListener

	mu     sync.Mutex
	conns  map[string]*peerConn
	closed bool
}

type peerConn struct {
	conn conn
	mu   sync.Mutex
}

func New(config transport.Config, local transport.Peer, handler transport.MsgHandler, services scm.Manager) (transport.Transport, error) {
	return newTransport(config, local, handler, clientAuthenticator{services: services})
}

func newTransport(config transport.Config, local transport.Peer, handler transport.MsgHandler, auth authenticator) (transport.Transport, error) {
	config = config.WithDefaults()

	t := &Transport{
		local:  local,
		config: config,
		auth:   auth,
		server: local.Type() == transport.PeerDaemon,
		conns:  make(map[string]*peerConn),
	}
	t.core = transport.NewCore(config, handler, t.send)

	if t.server {
		listener, err := listenPipe(daemonPipeName, daemonPipeACL)
		if err != nil {
			return nil, fmt.Errorf("listen on pipe for %s: %w", local, err)
		}
		t.listener = listener
		go t.acceptLoop()
	}
	slog.Info("pipe: created", "local", local.String(), "server", t.server)
	return t, nil
}

func (t *Transport) Target(typ transport.PeerType, opts ...transport.PeerOption) transport.Peer {
	return t.core.Target(typ, opts...)
}

func (t *Transport) Send(target transport.Peer, msg transport.Message, opts ...transport.Opt) error {
	return t.core.Send(target, msg, opts...)
}

func (t *Transport) Req(target transport.Peer, msg transport.Message, opts ...transport.Opt) (transport.Message, error) {
	return t.core.Request(target, msg, opts...)
}

func (t *Transport) Close() error {
	t.core.Close()

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	conns := make([]*peerConn, 0, len(t.conns))
	for name, pc := range t.conns {
		conns = append(conns, pc)
		delete(t.conns, name)
	}
	t.mu.Unlock()

	var err error
	if t.listener != nil {
		err = t.listener.Close()
	}
	for _, pc := range conns {
		pc.conn.Close()
	}
	return err
}

func (t *Transport) send(target transport.Peer, msg transport.Message, opts ...transport.Opt) error {
	if t.server {
		return t.sendToPeer(target, msg)
	}
	return t.sendToDaemon(msg, opts...)
}

func (t *Transport) sendToPeer(target transport.Peer, msg transport.Message) error {
	t.mu.Lock()
	pc := t.conns[target.Name()]
	t.mu.Unlock()
	if pc == nil {
		return fmt.Errorf("peer %s is not connected", target)
	}
	if err := t.write(pc, msg); err != nil {
		slog.Warn(pipeMsg(pc.conn.tag(), "write failed"), "local", t.local.Name(), "target", target.String(), "error", err)
		return fmt.Errorf("send to %s: %w", target, err)
	}
	slog.Debug(pipeMsg(pc.conn.tag(), "sent"), "local", t.local.Name(), "to", target.String(), "type", msg.Type, "id", msg.ID, "request_id", msg.RequestID)
	return nil
}

func (t *Transport) sendToDaemon(msg transport.Message, opts ...transport.Opt) error {
	pc, err := t.daemonConn(opts...)
	if err != nil {
		return err
	}
	if err := t.write(pc, msg); err != nil {
		slog.Warn(pipeMsg(pc.conn.tag(), "write failed"), "local", t.local.Name(), "target", transport.PeerDaemonName, "error", err)
		t.dropConn(transport.PeerDaemonName, pc)
		return fmt.Errorf("send to daemon: %w", err)
	}
	slog.Debug(pipeMsg(pc.conn.tag(), "sent"), "local", t.local.Name(), "to", transport.PeerDaemonName, "type", msg.Type, "id", msg.ID, "request_id", msg.RequestID)
	return nil
}

func (t *Transport) write(pc *peerConn, msg transport.Message) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return writeEnvelope(pc.conn, envelope{From: t.local.Name(), Msg: msg})
}

// daemonConn returns the connection to the daemon, dialing it on first use. The
// connection is kept until it fails; the next send reconnects.
func (t *Transport) daemonConn(opts ...transport.Opt) (*peerConn, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, transport.ErrClosed
	}
	if pc, ok := t.conns[transport.PeerDaemonName]; ok {
		return pc, nil
	}

	options := transport.ResolveOptions(opts...)
	timeout := t.config.ConnectTimeout
	if options.ConnectTimeout > 0 {
		timeout = options.ConnectTimeout
	}
	c, err := dialPipe(daemonPipeName, timeout)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	pc := &peerConn{conn: c}
	t.conns[transport.PeerDaemonName] = pc
	slog.Debug(pipeMsg(c.tag(), "dialed"), "local", t.local.Name(), "pipe", daemonPipeName)
	go t.serve(pc, transport.NewPeer(transport.PeerDaemon, transport.PeerDaemonName, false))
	return pc, nil
}

func (t *Transport) dropConn(name string, pc *peerConn) {
	t.mu.Lock()
	if t.conns[name] == pc {
		delete(t.conns, name)
	}
	t.mu.Unlock()
	pc.conn.Close()
}

func (t *Transport) acceptLoop() {
	for {
		c, err := t.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				slog.Debug("pipe: accept loop stopped", "local", t.local.Name(), "error", err)
				return
			}
			slog.Warn("pipe: accept failed", "local", t.local.Name(), "error", err)
			time.Sleep(10 * time.Millisecond)
			continue
		}
		peer, err := t.authenticate(c)
		if err != nil {
			slog.Warn(pipeMsg(c.tag(), "authentication failed"), "local", t.local.Name(), "error", err)
			c.Close()
			continue
		}

		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			c.Close()
			return
		}
		previous := t.conns[peer.Name()]
		pc := &peerConn{conn: c}
		t.conns[peer.Name()] = pc
		t.mu.Unlock()
		if previous != nil {
			previous.conn.Close()
		}
		slog.Debug(pipeMsg(c.tag(), "accepted"), "local", t.local.Name(), "peer", peer.String())
		go t.serve(pc, peer)
	}
}

func (t *Transport) serve(pc *peerConn, peer transport.Peer) {
	defer func() {
		pc.conn.Close()
		t.mu.Lock()
		if t.conns[peer.Name()] == pc {
			delete(t.conns, peer.Name())
		}
		t.mu.Unlock()
	}()

	slog.Debug(pipeMsg(pc.conn.tag(), "serve started"), "local", t.local.Name(), "peer", peer.String())
	decoder := json.NewDecoder(pc.conn)
	for {
		var env envelope
		if err := decoder.Decode(&env); err != nil {
			slog.Debug(pipeMsg(pc.conn.tag(), "connection closed"), "local", t.local.Name(), "error", err)
			return
		}
		slog.Debug(pipeMsg(pc.conn.tag(), "received"), "local", t.local.Name(),
			"from", env.From, "type", env.Msg.Type, "id", env.Msg.ID, "request_id", env.Msg.RequestID)
		if env.From != peer.Name() {
			slog.Warn(pipeMsg(pc.conn.tag(), "dropping message with mismatched sender"),
				"local", t.local.Name(), "from", env.From, "peer", peer.String())
			continue
		}
		if err := t.core.Dispatch(peer, env.Msg); err != nil {
			slog.Error(pipeMsg(pc.conn.tag(), "message handler failed"), "local", t.local.Name(), "from", peer.String(), "type", env.Msg.Type, "error", err)
		}
	}
}

func (t *Transport) authenticate(c conn) (transport.Peer, error) {
	if t.auth == nil {
		return transport.Peer{}, errors.New("transport has no authenticator")
	}
	return t.auth.authenticate(c.Handle(), time.Now())
}

// dialPipe connects to a named pipe, retrying until timeout while the server is
// momentarily between pipe instances (ERROR_FILE_NOT_FOUND) or busy.
func dialPipe(path string, timeout time.Duration) (*pipeConn, error) {
	namePtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		handle, err := windows.CreateFile(
			namePtr,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0,
			nil,
			windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED,
			0,
		)
		if err == nil {
			file, fileErr := winio.NewOpenFile(handle)
			if fileErr != nil {
				windows.CloseHandle(handle)
				return nil, fileErr
			}
			return newPipeConn(handle, file), nil
		}
		if time.Now().After(deadline) || !isRetryableDialError(err) {
			return nil, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func isRetryableDialError(err error) bool {
	if errors.Is(err, winio.ErrTimeout) {
		return true
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case errnoFileNotFound, errnoPipeBusy:
		return true
	default:
		return false
	}
}

func writeEnvelope(w io.Writer, env envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// pipeMsg prefixes a transport log message with a "[client->server]" pid tag.
func pipeMsg(tag, msg string) string {
	if tag == "" {
		return "pipe: " + msg
	}
	return fmt.Sprintf("pipe: %s %s", tag, msg)
}
