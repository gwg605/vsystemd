package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/scheduler"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/transport"
)

const daemonPeer = "daemon"

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

	loopStop    chan struct{}
	loopDone    chan struct{}
	loopStarted bool
}

func New(manager scm.Manager, store storage.Storage, binaryPath, logsDir string, logRotation logwriter.Rotation, newTransport transport.Factory) (*Daemon, error) {
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
		loopStop:    make(chan struct{}),
		loopDone:    make(chan struct{}),
	}
	tr, err := newTransport(transport.NewPeer(transport.PeerDaemon, daemonPeer, false), daemon.handle)
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
	d.startLoop()
	if err := d.startTimers(); err != nil {
		slog.Error("daemon: start timers failed", "error", err)
		return err
	}
	slog.Info("daemon: started")
	return nil
}

func (d *Daemon) Close() error {
	slog.Info("daemon: closing")

	d.mu.Lock()
	started := d.loopStarted
	if started {
		close(d.loopStop)
		d.loopStarted = false
	}
	d.mu.Unlock()
	if started {
		<-d.loopDone
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

func (d *Daemon) startLoop() {
	d.mu.Lock()
	if d.loopStarted {
		d.mu.Unlock()
		return
	}
	d.loopStarted = true
	d.mu.Unlock()

	go func() {
		defer close(d.loopDone)
		cleanup := time.NewTicker(time.Hour)
		defer cleanup.Stop()
		watchdog := time.NewTicker(config.HeartbeatInterval / 2)
		defer watchdog.Stop()
		for {
			select {
			case <-cleanup.C:
				d.cleanupLogs()
			case <-watchdog.C:
				d.checkRunners()
			case <-d.loopStop:
				return
			}
		}
	}()
}

// checkRunners marks a runner dead when its heartbeat has been silent for too
// long.
func (d *Daemon) checkRunners() {
	now := time.Now().UTC()
	d.mu.Lock()
	defer d.mu.Unlock()
	for name, state := range d.states {
		switch state.RunnerState {
		case RunnerStateActive, RunnerStateActivating, RunnerStateRestarting:
		default:
			continue
		}
		if now.Sub(state.LastSeen) < config.RunnerDeadAfter {
			continue
		}
		state.RunnerState = RunnerStateDead
		state.LastTransition = now
		state.LastSeen = now
		d.states[name] = state
		slog.Warn("daemon: runner heartbeat lost", "unit", name)
	}
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
