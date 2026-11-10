package transport

import (
	"encoding/json"
	"time"
)

type Message struct {
	ID        string          `json:"id"`
	RequestID string          `json:"request_id,omitempty"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type MsgHandler func(sourcePeer string, msg Message) error

type Transport interface {
	Send(targetPeer string, msg Message, opts ...Opt) error
	Req(targetPeer string, msg Message, opts ...Opt) (Message, error)
	Close() error
}

type Config struct {
	ConnectTimeout time.Duration
	IdleTimeout    time.Duration
	RequestTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{
		ConnectTimeout: 2 * time.Second,
		IdleTimeout:    5 * time.Second,
		RequestTimeout: 10 * time.Second,
	}
}

func (c Config) WithDefaults() Config {
	defaults := DefaultConfig()
	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = defaults.ConnectTimeout
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = defaults.IdleTimeout
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaults.RequestTimeout
	}
	return c
}

type Options struct {
	Timeout        time.Duration
	ConnectTimeout time.Duration
}

type Opt func(*Options)

func WithTimeout(d time.Duration) Opt {
	return func(o *Options) {
		o.Timeout = d
	}
}

func WithConnectTimeout(d time.Duration) Opt {
	return func(o *Options) {
		o.ConnectTimeout = d
	}
}

func ResolveOptions(opts ...Opt) Options {
	var options Options
	for _, opt := range opts {
		opt(&options)
	}
	return options
}
