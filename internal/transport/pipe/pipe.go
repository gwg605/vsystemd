//go:build windows

package pipe

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/gwg605/vsystemd/internal/transport"
)

const (
	daemonPeer        = "daemon"
	systemctlPrefix   = "systemctl-"
	daemonPipeName    = `\\.\pipe\vsystemd-in`
	servicePipePrefix = `\\.\pipe\vsystemd-svc-`
	controlPipePrefix = `\\.\pipe\vsystemd-ctl-`

	pipeACL       = "D:(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;OW)"
	daemonPipeACL = "D:(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;OW)(A;;GA;;;S-1-5-80-0)"
)

type envelope struct {
	From string            `json:"from"`
	Msg  transport.Message `json:"msg"`
}

type Transport struct {
	peerName string
	config   transport.Config
	core     *transport.Core
	listener net.Listener

	mu       sync.Mutex
	outbound map[string]*outbound
	inbound  map[net.Conn]struct{}
	closed   bool
}

type outbound struct {
	conn  net.Conn
	mu    sync.Mutex
	timer *time.Timer
}

func New(config transport.Config, peerName string, handler transport.MsgHandler) (transport.Transport, error) {
	config = config.WithDefaults()
	listener, err := winio.ListenPipe(pipeName(peerName), &winio.PipeConfig{
		SecurityDescriptor: pipeACLFor(peerName),
	})
	if err != nil {
		return nil, fmt.Errorf("listen on pipe for %s: %w", peerName, err)
	}

	t := &Transport{
		peerName: peerName,
		config:   config,
		listener: listener,
		outbound: make(map[string]*outbound),
		inbound:  make(map[net.Conn]struct{}),
	}
	t.core = transport.NewCore(config, handler, t.send)
	go t.acceptLoop()
	return t, nil
}

func (t *Transport) Send(targetPeer string, msg transport.Message, opts ...transport.Opt) error {
	return t.core.Send(targetPeer, msg, opts...)
}

func (t *Transport) Req(targetPeer string, msg transport.Message, opts ...transport.Opt) (transport.Message, error) {
	return t.core.Request(targetPeer, msg, opts...)
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
	inbound := make([]net.Conn, 0, len(t.inbound))
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

func (t *Transport) send(targetPeer string, msg transport.Message, opts ...transport.Opt) error {
	conn, err := t.outboundFor(targetPeer, opts...)
	if err != nil {
		return err
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if err := writeEnvelope(conn.conn, envelope{From: t.peerName, Msg: msg}); err != nil {
		t.dropOutbound(targetPeer, conn)
		return fmt.Errorf("send to %s: %w", targetPeer, err)
	}
	t.resetIdle(targetPeer, conn)
	return nil
}

func (t *Transport) outboundFor(targetPeer string, opts ...transport.Opt) (*outbound, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, transport.ErrClosed
	}
	if conn, ok := t.outbound[targetPeer]; ok {
		return conn, nil
	}

	options := transport.ResolveOptions(opts...)
	timeout := t.config.ConnectTimeout
	if options.ConnectTimeout > 0 {
		timeout = options.ConnectTimeout
	}
	conn, err := winio.DialPipe(pipeName(targetPeer), &timeout)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", targetPeer, err)
	}

	out := &outbound{conn: conn}
	t.outbound[targetPeer] = out
	return out, nil
}

func (t *Transport) resetIdle(targetPeer string, conn *outbound) {
	if t.config.IdleTimeout <= 0 {
		return
	}
	if conn.timer != nil {
		conn.timer.Stop()
	}
	conn.timer = time.AfterFunc(t.config.IdleTimeout, func() {
		t.closeIdle(targetPeer, conn)
	})
}

func (t *Transport) closeIdle(targetPeer string, conn *outbound) {
	t.mu.Lock()
	if t.outbound[targetPeer] == conn {
		delete(t.outbound, targetPeer)
	}
	t.mu.Unlock()

	conn.mu.Lock()
	conn.conn.Close()
	conn.mu.Unlock()
}

func (t *Transport) dropOutbound(targetPeer string, conn *outbound) {
	t.mu.Lock()
	if t.outbound[targetPeer] == conn {
		delete(t.outbound, targetPeer)
	}
	t.mu.Unlock()

	if conn.timer != nil {
		conn.timer.Stop()
	}
	conn.conn.Close()
}

func (t *Transport) acceptLoop() {
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			return
		}
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			conn.Close()
			return
		}
		t.inbound[conn] = struct{}{}
		t.mu.Unlock()
		go t.serve(conn)
	}
}

func (t *Transport) serve(conn net.Conn) {
	defer func() {
		conn.Close()
		t.mu.Lock()
		delete(t.inbound, conn)
		t.mu.Unlock()
	}()

	decoder := json.NewDecoder(conn)
	for {
		var env envelope
		if err := decoder.Decode(&env); err != nil {
			return
		}
		if env.From == "" {
			continue
		}
		_ = t.core.Dispatch(env.From, env.Msg)
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

func pipeName(peer string) string {
	switch {
	case peer == daemonPeer:
		return daemonPipeName
	case strings.HasPrefix(peer, systemctlPrefix):
		return controlPipePrefix + strings.TrimPrefix(peer, systemctlPrefix)
	default:
		return servicePipePrefix + peer
	}
}

func pipeACLFor(peer string) string {
	if peer == daemonPeer {
		return daemonPipeACL
	}
	return pipeACL
}
