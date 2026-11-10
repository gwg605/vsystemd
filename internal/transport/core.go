package transport

import (
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrClosed = errors.New("transport closed")

type SendFunc func(targetPeer string, msg Message, opts ...Opt) error

type Core struct {
	config  Config
	handler MsgHandler
	send    SendFunc

	mu      sync.Mutex
	pending map[string]chan Message
	closed  bool
	done    chan struct{}
}

func NewCore(config Config, handler MsgHandler, send SendFunc) *Core {
	return &Core{
		config:  config.WithDefaults(),
		handler: handler,
		send:    send,
		pending: make(map[string]chan Message),
		done:    make(chan struct{}),
	}
}

func (c *Core) Send(targetPeer string, msg Message, opts ...Opt) error {
	if msg.ID == "" {
		id, err := newID()
		if err != nil {
			return fmt.Errorf("generate message id: %w", err)
		}
		msg.ID = id
	}

	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}

	return c.send(targetPeer, msg, opts...)
}

func (c *Core) Request(targetPeer string, msg Message, opts ...Opt) (Message, error) {
	if msg.ID == "" {
		id, err := newID()
		if err != nil {
			return Message{}, fmt.Errorf("generate message id: %w", err)
		}
		msg.ID = id
	}

	options := ResolveOptions(opts...)
	timeout := c.config.RequestTimeout
	if options.Timeout > 0 {
		timeout = options.Timeout
	}

	response := make(chan Message, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Message{}, ErrClosed
	}
	c.pending[msg.ID] = response
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, msg.ID)
		c.mu.Unlock()
	}()

	if err := c.Send(targetPeer, msg, opts...); err != nil {
		return Message{}, err
	}

	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	select {
	case resp := <-response:
		return resp, nil
	case <-timeoutCh:
		return Message{}, fmt.Errorf("request %s to %s timed out after %s", msg.Type, targetPeer, timeout)
	case <-c.done:
		return Message{}, ErrClosed
	}
}

func (c *Core) Dispatch(sourcePeer string, msg Message) error {
	if msg.RequestID != "" {
		c.mu.Lock()
		response, ok := c.pending[msg.RequestID]
		c.mu.Unlock()
		if ok {
			select {
			case response <- msg:
			default:
			}
			return nil
		}
		return nil
	}
	if c.handler == nil {
		return nil
	}
	return c.handler(sourcePeer, msg)
}

func (c *Core) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.done)
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
