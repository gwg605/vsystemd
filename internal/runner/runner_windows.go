//go:build windows

package runner

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

type TransportFactory func(local transport.Peer, handler transport.MsgHandler) (transport.Transport, error)

type Runner struct {
	name      string
	logs      *LogStreamer
	handler   *SCMHandler
	reporter  *StateReporter
	transport transport.Transport
}

func NewRunner(name string, service *unit.ServiceUnit, logs *LogStreamer, env EnvironmentDefaults, newTransport TransportFactory) (*Runner, error) {
	if newTransport == nil {
		return nil, errors.New("transport factory is required")
	}

	if logs == nil {
		return nil, errors.New("log streamer is required")
	}

	process := NewProcessManager(service, env)
	process.SetLogStreamer(logs)
	restart := NewRestartController(service)
	handler := NewSCMHandler(service, process, restart)

	runner := &Runner{
		name:    name,
		logs:    logs,
		handler: handler,
	}

	tr, err := newTransport(transport.NewPeer(transport.PeerRunner, name, false), runner.handle)
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
	runner.handler.SetStateReporter(runner.reporter)
	slog.Info("runner: created", "unit", name)
	return runner, nil
}

func (r *Runner) handle(source transport.Peer, msg transport.Message) error {
	return r.reporter.Handle(source, msg)
}

func (r *Runner) Run() error {
	serviceName := strings.TrimSuffix(r.name, filepath.Ext(r.name))
	return Run(serviceName, r.handler)
}

func (r *Runner) Serve(requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	return r.handler.Execute(nil, requests, changes)
}

func (r *Runner) RunConsole() error {
	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status)
	done := make(chan struct{})

	go func() {
		r.handler.Execute(nil, requests, changes)
		close(done)
	}()
	go func() {
		for range changes {
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)

	select {
	case <-signals:
		select {
		case requests <- svc.ChangeRequest{Cmd: svc.Stop}:
		case <-done:
		}
		<-done
	case <-done:
	}
	close(changes)
	return nil
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
