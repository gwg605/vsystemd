//go:build windows

package runner

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

type TransportFactory func(peerName string, handler transport.MsgHandler) (transport.Transport, error)

type Runner struct {
	name      string
	logs      *LogStreamer
	handler   *SCMHandler
	reporter  *StateReporter
	transport transport.Transport
}

func NewRunner(name string, service *unit.ServiceUnit, store storage.Storage, newTransport TransportFactory) (*Runner, error) {
	if newTransport == nil {
		return nil, errors.New("transport factory is required")
	}

	logs, err := NewLogStreamer(name, service, store)
	if err != nil {
		return nil, err
	}

	process := NewProcessManager(service)
	process.SetLogStreamer(logs)
	restart := NewRestartController(service)
	handler := NewSCMHandler(service, process, restart)

	runner := &Runner{
		name:    name,
		logs:    logs,
		handler: handler,
	}

	tr, err := newTransport(name, runner.handle)
	if err != nil {
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
	return runner, nil
}

func (r *Runner) handle(sourcePeer string, msg transport.Message) error {
	return r.reporter.Handle(sourcePeer, msg)
}

func (r *Runner) Run() error {
	serviceName := strings.TrimSuffix(r.name, filepath.Ext(r.name))
	return Run(serviceName, r.handler)
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
	transportErr := r.transport.Close()
	logsErr := r.logs.Close()
	if transportErr != nil {
		return transportErr
	}
	return logsErr
}
