//go:build windows

package runner

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/unit"
)

func TestSCMHandlerReportsStates(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	dir := t.TempDir()

	reporter, _, collector := newReporterPair(t, "app.service")
	service := &unit.ServiceUnit{
		ExecStart: helperCommand("record", filepath.Join(dir, "exit.txt"), "done", "3"),
	}
	process := NewProcessManager(service)
	handler := NewSCMHandler(service, process, NewRestartController(service))
	handler.SetStateReporter(reporter)

	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 20)
	done := make(chan struct{})
	go func() {
		handler.Execute(nil, requests, changes)
		close(done)
	}()

	waitForStatus(t, changes, svc.Stopped)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not return")
	}
	close(collector.events)

	var states []string
	for event := range collector.events {
		states = append(states, event.State)
	}
	if len(states) < 3 {
		t.Fatalf("states = %v", states)
	}
	if states[0] != StateActivating || states[1] != StateActive {
		t.Errorf("states = %v", states)
	}
	if states[len(states)-1] != StateFailed {
		t.Errorf("last state = %q", states[len(states)-1])
	}
}
