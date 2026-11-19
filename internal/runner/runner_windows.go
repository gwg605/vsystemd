//go:build windows

package runner

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

type Runner struct {
	name      string
	logs      *LogStreamer
	handler   *controlHandler
	reporter  *StateReporter
	transport transport.Transport
}

func NewRunner(name string, service *unit.ServiceUnit, logs *LogStreamer, env EnvironmentDefaults, newTransport transport.Factory) (*Runner, error) {
	if newTransport == nil {
		return nil, errors.New("transport factory is required")
	}

	if logs == nil {
		return nil, errors.New("log streamer is required")
	}

	restart := NewRestartController(service)
	handler := newControlHandler(service, restart, env, logs)

	runner := &Runner{
		name:    name,
		logs:    logs,
		handler: handler,
	}

	tr, err := newTransport(transport.NewPeer(transport.PeerRunner, name, false), nil)
	if err != nil {
		slog.Error("runner: create transport failed", "unit", name, "error", err)
		logs.Close()
		return nil, fmt.Errorf("create transport: %w", err)
	}
	if tr == nil {
		logs.Close()
		return nil, errors.New("transport factory returned nil")
	}

	runner.transport = tr
	runner.reporter = NewStateReporter(tr, name)
	runner.handler.setReporter(runner.reporter)
	slog.Info("runner: created", "unit", name)
	return runner, nil
}

func (r *Runner) Run() error {
	stop := make(chan struct{})
	go r.heartbeat(stop)
	defer close(stop)

	serviceName := strings.TrimSuffix(r.name, filepath.Ext(r.name))
	return Run(serviceName, r.handler)
}

func (r *Runner) heartbeat(stop <-chan struct{}) {
	ticker := time.NewTicker(config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := r.reporter.Heartbeat(); err != nil {
				slog.Warn("runner: heartbeat failed", "unit", r.name, "error", err)
			}
		case <-stop:
			return
		}
	}
}

func (r *Runner) Serve(requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	return r.handler.Execute(nil, requests, changes)
}

func (r *Runner) Close() error {
	slog.Info("runner: closing", "unit", r.name)
	transportErr := r.transport.Close()
	logsErr := r.logs.Close()
	if transportErr != nil {
		slog.Warn("runner: close transport failed", "unit", r.name, "error", transportErr)
		return transportErr
	}
	if logsErr != nil {
		slog.Warn("runner: close logs failed", "unit", r.name, "error", logsErr)
	}
	return logsErr
}
