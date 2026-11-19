package transport

import (
	"encoding/json"
	"time"

	"github.com/gwg605/vsystemd/internal/scm"
)

// PeerType classifies the process on the other end of a connection.
type PeerType uint8

const (
	PeerUnknown PeerType = iota
	PeerDaemon           // the vsystemd daemon
	PeerRunner           // a service runner
	PeerControl          // a systemctl control client
)

// PeerDaemonName is the canonical name of the daemon peer.
const PeerDaemonName = "daemon"

func (t PeerType) String() string {
	switch t {
	case PeerDaemon:
		return "daemon"
	case PeerRunner:
		return "runner"
	case PeerControl:
		return "control"
	default:
		return "unknown"
	}
}

// Peer is a transport-verified identity of a peer.
//
// NewPeer is intended for Transport implementations. Application code must
// obtain peers from a message handler (the verified source) or from
// Transport.Target (an outbound target), never by constructing them directly.
type Peer struct {
	typ      PeerType
	name     string
	elevated bool
	service  scm.ServiceConfig
}

func NewPeer(typ PeerType, name string, elevated bool) Peer {
	return Peer{typ: typ, name: name, elevated: elevated}
}

// WithService returns a copy of the peer carrying the authenticated service
// configuration of a runner.
func (p Peer) WithService(cfg scm.ServiceConfig) Peer {
	p.service = cfg
	return p
}

func (p Peer) Type() PeerType { return p.typ }
func (p Peer) Name() string   { return p.name }
func (p Peer) Elevated() bool { return p.elevated }

// ServiceConfig returns the authenticated service configuration carried by a
// runner peer; it is zero for other peers.
func (p Peer) ServiceConfig() scm.ServiceConfig { return p.service }

func (p Peer) String() string { return p.typ.String() + ":" + p.name }

// Valid reports whether the peer has a known type and a name.
func (p Peer) Valid() bool { return p.typ != PeerUnknown && p.name != "" }

func (p Peer) equal(other Peer) bool {
	return p.typ == other.typ && p.name == other.name
}

type Message struct {
	ID        string          `json:"id"`
	RequestID string          `json:"request_id,omitempty"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// MsgHandler receives messages together with the transport-verified source peer.
type MsgHandler func(source Peer, msg Message) error

// Transport is the peer-centric public interface used by application components.
type Transport interface {
	// Target returns an outbound target peer. PeerType is required; additional
	// peer-specific parameters are supplied through options (e.g. WithName).
	Target(typ PeerType, opts ...PeerOption) Peer

	Send(target Peer, msg Message, opts ...Opt) error
	Req(target Peer, msg Message, opts ...Opt) (Message, error)
	Close() error
}

// Factory builds a Transport for a local peer and message handler.
type Factory func(local Peer, handler MsgHandler) (Transport, error)

type peerOptions struct {
	name string
}

// PeerOption configures an outbound target peer.
type PeerOption func(*peerOptions)

// WithName sets the target peer name (for example the unit name of a runner).
func WithName(name string) PeerOption {
	return func(o *peerOptions) { o.name = name }
}

func resolvePeerOptions(opts ...PeerOption) peerOptions {
	var options peerOptions
	for _, opt := range opts {
		opt(&options)
	}
	return options
}

type Config struct {
	ConnectTimeout time.Duration
	RequestTimeout time.Duration
	MaxMessageSize int
}

func DefaultConfig() Config {
	return Config{
		ConnectTimeout: 2 * time.Second,
		RequestTimeout: 10 * time.Second,
		MaxMessageSize: 4 << 20,
	}
}

func (c Config) WithDefaults() Config {
	defaults := DefaultConfig()
	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = defaults.ConnectTimeout
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaults.RequestTimeout
	}
	if c.MaxMessageSize <= 0 {
		c.MaxMessageSize = defaults.MaxMessageSize
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
