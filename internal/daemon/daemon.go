package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/scheduler"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/transport"
)

const daemonPeer = "daemon"

type TransportFactory func(peerName string, handler transport.MsgHandler) (transport.Transport, error)

type Daemon struct {
	manager     scm.Manager
	store       storage.Storage
	binaryPath  string
	logsDir     string
	logRotation logwriter.Rotation
	transport   transport.Transport
	sched       *scheduler.Scheduler

	mu     sync.Mutex
	states map[string]ServiceState

	timerMu     sync.Mutex
	knownTimers map[string]bool

	cleanupStop    chan struct{}
	cleanupDone    chan struct{}
	cleanupStarted bool
}

func New(manager scm.Manager, store storage.Storage, binaryPath, logsDir string, logRotation logwriter.Rotation, newTransport TransportFactory) (*Daemon, error) {
	if newTransport == nil {
		return nil, errors.New("transport factory is required")
	}

	daemon := &Daemon{
		manager:     manager,
		store:       store,
		binaryPath:  binaryPath,
		logsDir:     logsDir,
		logRotation: logRotation,
		states:      make(map[string]ServiceState),
		sched:       scheduler.New(manager, store),
		knownTimers: make(map[string]bool),
		cleanupStop: make(chan struct{}),
		cleanupDone: make(chan struct{}),
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
	d.cleanupLogs()
	d.startCleanupLoop()
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

	d.mu.Lock()
	started := d.cleanupStarted
	if started {
		close(d.cleanupStop)
		d.cleanupStarted = false
	}
	d.mu.Unlock()
	if started {
		<-d.cleanupDone
	}

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

func (d *Daemon) startCleanupLoop() {
	d.mu.Lock()
	if d.cleanupStarted {
		d.mu.Unlock()
		return
	}
	d.cleanupStarted = true
	d.mu.Unlock()

	go func() {
		defer close(d.cleanupDone)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				d.cleanupLogs()
			case <-d.cleanupStop:
				return
			}
		}
	}()
}

func (d *Daemon) cleanupLogs() {
	if d.logRotation.MaxAgeDays <= 0 {
		return
	}

	now := time.Now().UTC()
	if err := logwriter.Cleanup(filepath.Join(d.logsDir, "vsystemd"), d.logRotation.MaxAgeDays, now); err != nil {
		slog.Debug("daemon: cleanup operation logs failed", "error", err)
	}

	entries, err := os.ReadDir(d.logsDir)
	if err != nil {
		slog.Debug("daemon: list log directories failed", "error", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "vsystemd" {
			continue
		}
		dir := filepath.Join(d.logsDir, entry.Name())
		if err := logwriter.Cleanup(dir, d.logRotation.MaxAgeDays, now); err != nil {
			slog.Debug("daemon: cleanup service logs failed", "dir", dir, "error", err)
		}
	}
}
