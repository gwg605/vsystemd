//go:build windows

package runner

import (
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

type Runner struct {
	name     string
	logs     *LogStreamer
	handler  *SCMHandler
	reporter *StateReporter
}

func NewRunner(name string, service *unit.ServiceUnit, store storage.Storage) (*Runner, error) {
	logs, err := NewLogStreamer(name, service, store)
	if err != nil {
		return nil, err
	}

	process := NewProcessManager(service)
	process.SetLogStreamer(logs)
	restart := NewRestartController(service)
	handler := NewSCMHandler(service, process, restart)

	return &Runner{
		name:    name,
		logs:    logs,
		handler: handler,
	}, nil
}

func (r *Runner) SetTransport(tr transport.Transport) {
	r.reporter = NewStateReporter(tr, r.name)
	r.handler.SetStateReporter(r.reporter)
}

func (r *Runner) Handle(sourcePeer string, msg transport.Message) error {
	if r.reporter == nil {
		return nil
	}
	return r.reporter.Handle(sourcePeer, msg)
}

func (r *Runner) Run() error {
	return Run(r.name, r.handler)
}

func (r *Runner) Close() error {
	return r.logs.Close()
}
