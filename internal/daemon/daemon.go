package daemon

import (
	"errors"
	"fmt"
	"sync"

	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/transport"
)

const daemonPeer = "daemon"

type TransportFactory func(peerName string, handler transport.MsgHandler) (transport.Transport, error)

type Daemon struct {
	manager    scm.Manager
	store      storage.Storage
	binaryPath string
	transport  transport.Transport

	mu     sync.Mutex
	states map[string]ServiceState

	timerMu sync.Mutex
	timers  map[string]*timerRuntime
}

func New(manager scm.Manager, store storage.Storage, binaryPath string, newTransport TransportFactory) (*Daemon, error) {
	if newTransport == nil {
		return nil, errors.New("transport factory is required")
	}

	daemon := &Daemon{
		manager:    manager,
		store:      store,
		binaryPath: binaryPath,
		states:     make(map[string]ServiceState),
		timers:     make(map[string]*timerRuntime),
	}
	tr, err := newTransport(daemonPeer, daemon.handle)
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}
	if tr == nil {
		return nil, errors.New("transport factory returned nil")
	}
	daemon.transport = tr
	return daemon, nil
}

func (d *Daemon) Start() error {
	if err := d.startTimers(); err != nil {
		return err
	}
	return d.requestRunnerStates()
}

func (d *Daemon) Close() error {
	return d.transport.Close()
}
