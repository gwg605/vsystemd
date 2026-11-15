//go:build windows

package pipe

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/gwg605/vsystemd/internal/transport"
)

// errnoFileNotFound is ERROR_FILE_NOT_FOUND and errnoPipeBusy is ERROR_PIPE_BUSY.
const (
	errnoFileNotFound = syscall.Errno(2)
	errnoPipeBusy     = syscall.Errno(231)
)

const (
	servicePipePrefix = `\\.\pipe\vsystemd-svc-`
	controlPipePrefix = `\\.\pipe\vsystemd-ctl-`

	pipeACL       = "D:(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;OW)"
	daemonPipeACL = "D:(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;OW)(A;;GA;;;S-1-5-80-0)"

	systemctlUnderscore = "systemctl_"
	systemctlHyphen     = "systemctl-"
)

// daemonPipeName is a variable so tests can avoid clashing with a running daemon.
var daemonPipeName = `\\.\pipe\vsystemd-in`

type envelope struct {
	From string            `json:"from"`
	Msg  transport.Message `json:"msg"`
}

type Transport struct {
	local    transport.Peer
	config   transport.Config
	core     *transport.Core
	listener *pipeListener
	auth     authenticator

	mu       sync.Mutex
	outbound map[string]*outbound
	inbound  map[conn]struct{}
	closed   bool
}

type outbound struct {
	conn  net.Conn
	mu    sync.Mutex
	timer *time.Timer
}

func New(config transport.Config, local transport.Peer, handler transport.MsgHandler) (transport.Transport, error) {
	return newWithAuth(config, local, handler, authenticatorFor(local))
}

func newWithAuth(config transport.Config, local transport.Peer, handler transport.MsgHandler, auth authenticator) (transport.Transport, error) {
	config = config.WithDefaults()
	listener, err := listenPipe(pipeName(local), pipeACLFor(local))
	if err != nil {
		return nil, fmt.Errorf("listen on pipe for %s: %w", local, err)
	}

	t := &Transport{
		local:    local,
		config:   config,
		listener: listener,
		auth:     auth,
		outbound: make(map[string]*outbound),
		inbound:  make(map[conn]struct{}),
	}
	t.core = transport.NewCore(config, handler, t.send)
	slog.Info("pipe: listening", "local", local.String(), "pipe", pipeName(local))
	go t.acceptLoop()
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
	connections := make([]*outbound, 0, len(t.outbound))
	for target, conn := range t.outbound {
		connections = append(connections, conn)
		delete(t.outbound, target)
	}
	inbound := make([]conn, 0, len(t.inbound))
	for conn := range t.inbound {
		inbound = append(inbound, conn)
		delete(t.inbound, conn)
	}
	t.mu.Unlock()

	err := t.listener.Close()
	for _, conn := range inbound {
		conn.Close()
	}
	for _, conn := range connections {
		if conn.timer != nil {
			conn.timer.Stop()
		}
		conn.mu.Lock()
		conn.conn.Close()
		conn.mu.Unlock()
	}
	return err
}

func (t *Transport) send(target transport.Peer, msg transport.Message, opts ...transport.Opt) error {
	conn, err := t.outboundFor(target, opts...)
	if err != nil {
		slog.Debug("pipe: connect failed", "peer", t.local.Name(), "target", target.Name(), "error", err)
		return err
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if err := writeEnvelope(conn.conn, envelope{From: t.local.Name(), Msg: msg}); err != nil {
		slog.Warn("pipe: write failed", "peer", t.local.Name(), "target", target.Name(), "error", err)
		t.dropOutbound(target.Name(), conn)
		return fmt.Errorf("send to %s: %w", target, err)
	}
	slog.Debug("pipe: sent", "from", t.local.Name(), "to", target.String(), "type", msg.Type, "id", msg.ID, "request_id", msg.RequestID)
	t.resetIdle(target.Name(), conn)
	return nil
}

func (t *Transport) outboundFor(target transport.Peer, opts ...transport.Opt) (*outbound, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, transport.ErrClosed
	}
	if conn, ok := t.outbound[target.Name()]; ok {
		return conn, nil
	}

	options := transport.ResolveOptions(opts...)
	timeout := t.config.ConnectTimeout
	if options.ConnectTimeout > 0 {
		timeout = options.ConnectTimeout
	}
	conn, err := dialPipe(pipeName(target), timeout)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", target, err)
	}
	slog.Debug("pipe: dialed", "from", t.local.Name(), "to", target.String(), "pipe", pipeName(target))

	out := &outbound{conn: conn}
	t.outbound[target.Name()] = out
	return out, nil
}

// dialPipe connects to a named pipe, retrying until timeout while the server is
// momentarily between pipe instances (ERROR_FILE_NOT_FOUND) or busy.
func dialPipe(path string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	attempt := 100 * time.Millisecond
	for {
		conn, err := winio.DialPipe(path, &attempt)
		if err == nil {
			return conn, nil
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

func (t *Transport) resetIdle(target string, conn *outbound) {
	if t.config.IdleTimeout <= 0 {
		return
	}
	if conn.timer != nil {
		conn.timer.Stop()
	}
	conn.timer = time.AfterFunc(t.config.IdleTimeout, func() {
		t.closeIdle(target, conn)
	})
}

func (t *Transport) closeIdle(target string, conn *outbound) {
	t.mu.Lock()
	if t.outbound[target] == conn {
		delete(t.outbound, target)
	}
	t.mu.Unlock()

	conn.mu.Lock()
	conn.conn.Close()
	conn.mu.Unlock()
}

func (t *Transport) dropOutbound(target string, conn *outbound) {
	t.mu.Lock()
	if t.outbound[target] == conn {
		delete(t.outbound, target)
	}
	t.mu.Unlock()

	if conn.timer != nil {
		conn.timer.Stop()
	}
	conn.conn.Close()
}

func (t *Transport) acceptLoop() {
	for {
		c, err := t.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				slog.Debug("pipe: accept loop stopped", "peer", t.local.Name(), "error", err)
				return
			}
			slog.Warn("pipe: accept failed", "peer", t.local.Name(), "error", err)
			time.Sleep(10 * time.Millisecond)
			continue
		}
		peer, err := t.authenticate(c)
		if err != nil {
			slog.Warn("pipe: authentication failed", "local", t.local.Name(), "error", err)
			c.Close()
			continue
		}
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			c.Close()
			return
		}
		t.inbound[c] = struct{}{}
		t.mu.Unlock()
		slog.Debug("pipe: accepted", "local", t.local.Name(), "peer", peer.String())
		go t.serve(c, peer)
	}
}

func (t *Transport) authenticate(c conn) (transport.Peer, error) {
	sc, ok := c.(*serverConn)
	if !ok {
		return transport.Peer{}, errors.New("connection has no handle")
	}
	if t.auth == nil {
		return transport.Peer{}, errors.New("transport has no authenticator")
	}
	return t.auth.authenticate(sc.Handle(), time.Now())
}

func (t *Transport) serve(c conn, peer transport.Peer) {
	defer func() {
		c.Close()
		t.mu.Lock()
		delete(t.inbound, c)
		t.mu.Unlock()
	}()

	slog.Debug("pipe: serve started", "local", t.local.Name(), "peer", peer.String())
	decoder := json.NewDecoder(c)
	for {
		var env envelope
		if err := decoder.Decode(&env); err != nil {
			slog.Debug("pipe: connection closed", "local", t.local.Name(), "error", err)
			return
		}
		slog.Debug("pipe: received", "local", t.local.Name(),
			"from", env.From, "type", env.Msg.Type, "id", env.Msg.ID, "request_id", env.Msg.RequestID)
		if env.From != peer.Name() {
			slog.Warn("pipe: dropping message with mismatched sender",
				"local", t.local.Name(), "from", env.From, "peer", peer.String())
			continue
		}
		if err := t.core.Dispatch(peer, env.Msg); err != nil {
			slog.Error("pipe: message handler failed", "peer", t.local.Name(), "from", peer.String(), "type", env.Msg.Type, "error", err)
		}
	}
}

func writeEnvelope(conn net.Conn, env envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	return err
}

func pipeName(peer transport.Peer) string {
	switch peer.Type() {
	case transport.PeerDaemon:
		return daemonPipeName
	case transport.PeerControl:
		return controlPipePrefix + controlSuffix(peer.Name())
	default:
		return servicePipePrefix + peer.Name()
	}
}

func pipeACLFor(local transport.Peer) string {
	if local.Type() == transport.PeerDaemon {
		return daemonPipeACL
	}
	return pipeACL
}

func controlSuffix(name string) string {
	if suffix, ok := strings.CutPrefix(name, systemctlUnderscore); ok {
		return suffix
	}
	if suffix, ok := strings.CutPrefix(name, systemctlHyphen); ok {
		return suffix
	}
	return name
}
