//go:build windows

package runner

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/unit"
)

func TestControlHandlerReportsStates(t *testing.T) {
	dir := t.TempDir()

	reporter, _, collector := newReporterPair(t, "app.service")
	service := &unit.ServiceUnit{
		ExecStart: helperCommand("record", filepath.Join(dir, "exit.txt"), "done", "3"),
	}
	handler := newTestHandler(service)
	handler.setReporter(reporter)

	changes, _, done := runControlHandler(t, handler)

	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)
	close(collector.events)

	var states []protocol.State
	for event := range collector.events {
		states = append(states, event.State)
	}
	if len(states) < 3 {
		t.Fatalf("states = %v", states)
	}
	if states[0] != protocol.StateActivating || states[1] != protocol.StateActive {
		t.Errorf("states = %v", states)
	}
	if states[len(states)-1] != protocol.StateFailed {
		t.Errorf("last state = %q", states[len(states)-1])
	}
}
