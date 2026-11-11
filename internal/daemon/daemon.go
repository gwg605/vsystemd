package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/gwg605/vsystemd/internal/scheduler"
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
	sched      *scheduler.Scheduler

	mu     sync.Mutex
	states map[string]ServiceState

	timerMu     sync.Mutex
	knownTimers map[string]bool
}

func New(manager scm.Manager, store storage.Storage, binaryPath string, newTransport TransportFactory) (*Daemon, error) {
	if newTransport == nil {
		return nil, errors.New("transport factory is required")
	}

	daemon := &Daemon{
		manager:     manager,
		store:       store,
		binaryPath:  binaryPath,
		states:      make(map[string]ServiceState),
		sched:       scheduler.New(manager, store),
		knownTimers: make(map[string]bool),
	}
	tr, err := newTransport(daemonPeer, daemon.handle)
	if err != nil {
		slog.Error("daemon: create transport failed", "error", err)
		return nil, fmt.Errorf("create transport: %w", err)
	}
	if tr == nil {
		return nil, errors.New("transport factory returned nil")
	}
	daemon.transport = tr
	slog.Info("daemon: created", "binary", binaryPath)
	return daemon, nil
}

func (d *Daemon) Start() error {
	slog.Info("daemon: starting")
	if err := d.startTimers(); err != nil {
		slog.Error("daemon: start timers failed", "error", err)
		return err
	}
	if err := d.requestRunnerStates(); err != nil {
		slog.Error("daemon: request runner states failed", "error", err)
		return err
	}
	slog.Info("daemon: started")
	return nil
}

func (d *Daemon) Close() error {
	slog.Info("daemon: closing")
	schedulerErr := d.sched.Close()
	transportErr := d.transport.Close()
	if schedulerErr != nil {
		slog.Warn("daemon: close scheduler failed", "error", schedulerErr)
		return schedulerErr
	}
	if transportErr != nil {
		slog.Warn("daemon: close transport failed", "error", transportErr)
	}
	return transportErr
}
