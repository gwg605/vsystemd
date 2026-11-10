//go:build windows

package runner

import (
	"errors"
	"fmt"

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
	return Run(r.name, r.handler)
}

func (r *Runner) Close() error {
	transportErr := r.transport.Close()
	logsErr := r.logs.Close()
	if transportErr != nil {
		return transportErr
	}
	return logsErr
}
