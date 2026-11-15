package fake

import (
	"fmt"
	"sync"

	"github.com/gwg605/vsystemd/internal/transport"
)

type Network struct {
	mu    sync.Mutex
	cores map[string]*transport.Core
}

func NewNetwork() *Network {
	return &Network{cores: make(map[string]*transport.Core)}
}

func (n *Network) New(config transport.Config, local transport.Peer, handler transport.MsgHandler) *Transport {
	t := &Transport{
		network: n,
		local:   local,
	}
	t.core = transport.NewCore(config, handler, t.send)
	n.mu.Lock()
	n.cores[local.Name()] = t.core
	n.mu.Unlock()
	return t
}

type Transport struct {
	network *Network
	local   transport.Peer
	core    *transport.Core
}

var _ transport.Transport = (*Transport)(nil)

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
	t.network.mu.Lock()
	if t.network.cores[t.local.Name()] == t.core {
		delete(t.network.cores, t.local.Name())
	}
	t.network.mu.Unlock()
	return nil
}

func (t *Transport) send(target transport.Peer, msg transport.Message, _ ...transport.Opt) error {
	t.network.mu.Lock()
	core := t.network.cores[target.Name()]
	t.network.mu.Unlock()
	if core == nil {
		return fmt.Errorf("peer %s not found", target.Name())
	}
	return core.Dispatch(t.local, msg)
}
