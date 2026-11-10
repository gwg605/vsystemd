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

func (n *Network) New(config transport.Config, peerName string, handler transport.MsgHandler) *Transport {
	t := &Transport{
		network:  n,
		peerName: peerName,
	}
	t.core = transport.NewCore(config, handler, t.send)
	n.mu.Lock()
	n.cores[peerName] = t.core
	n.mu.Unlock()
	return t
}

type Transport struct {
	network  *Network
	peerName string
	core     *transport.Core
}

var _ transport.Transport = (*Transport)(nil)

func (t *Transport) Send(targetPeer string, msg transport.Message, opts ...transport.Opt) error {
	return t.core.Send(targetPeer, msg, opts...)
}

func (t *Transport) Req(targetPeer string, msg transport.Message, opts ...transport.Opt) (transport.Message, error) {
	return t.core.Request(targetPeer, msg, opts...)
}

func (t *Transport) Close() error {
	t.core.Close()
	t.network.mu.Lock()
	if t.network.cores[t.peerName] == t.core {
		delete(t.network.cores, t.peerName)
	}
	t.network.mu.Unlock()
	return nil
}

func (t *Transport) send(targetPeer string, msg transport.Message, _ ...transport.Opt) error {
	t.network.mu.Lock()
	core := t.network.cores[targetPeer]
	t.network.mu.Unlock()
	if core == nil {
		return fmt.Errorf("peer %s not found", targetPeer)
	}
	return core.Dispatch(t.peerName, msg)
}
