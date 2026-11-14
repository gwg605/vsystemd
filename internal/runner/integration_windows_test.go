//go:build windows

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/transport"
	transportfake "github.com/gwg605/vsystemd/internal/transport/fake"
	"github.com/gwg605/vsystemd/internal/unit"
)

func newIntegratedRunner(t *testing.T, name string, service *unit.ServiceUnit) (*Runner, *stateCollector) {
	t.Helper()

	network := transportfake.NewNetwork()
	collector := newStateCollector()
	daemon := network.New(transport.DefaultConfig(), daemonPeer, collector.handler)
	t.Cleanup(func() { daemon.Close() })

	serviceName := strings.TrimSuffix(name, filepath.Ext(name))
	logs, err := NewLogStreamer(serviceName, service, t.TempDir(), logwriter.Rotation{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}

	factory := func(peer string, handler transport.MsgHandler) (transport.Transport, error) {
		return network.New(transport.DefaultConfig(), peer, handler), nil
	}
	runner, err := NewRunner(name, service, logs, factory)
	if err != nil {
		logs.Close()
		t.Fatalf("NewRunner() error = %v", err)
	}
	t.Cleanup(func() { runner.Close() })

	return runner, collector
}

func runSCMHandler(t *testing.T, handler *SCMHandler) (<-chan svc.Status, chan<- svc.ChangeRequest, <-chan struct{}) {
	t.Helper()
	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 20)
	done := make(chan struct{})
	go func() {
		handler.Execute(nil, requests, changes)
		close(done)
	}()
	return changes, requests, done
}

func waitForDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not return")
	}
}

func collectStates(collector *stateCollector) []StateEvent {
	close(collector.events)
	var states []StateEvent
	for event := range collector.events {
		states = append(states, event)
	}
	return states
}

func TestRunnerStartStop(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	dir := t.TempDir()
	stopFile := filepath.Join(dir, "stop.txt")

	service := &unit.ServiceUnit{
		ExecStart:      helperCommand("sleep", "5s"),
		ExecStop:       helperCommand("record", stopFile, "stopped"),
		TimeoutStopSec: 100 * time.Millisecond,
	}
	runner, collector := newIntegratedRunner(t, "app.service", service)
	changes, requests, done := runSCMHandler(t, runner.handler)

	waitForStatus(t, changes, svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	states := collectStates(collector)
	if len(states) < 3 {
		t.Fatalf("states = %v", states)
	}
	if states[0].State != StateActivating || states[1].State != StateActive {
		t.Errorf("states = %v", states)
	}
	if states[len(states)-1].State != StateInactive {
		t.Errorf("last state = %q", states[len(states)-1].State)
	}
	if data := waitForFile(t, stopFile); string(data) != "stopped\n" {
		t.Errorf("ExecStop output = %q", data)
	}
}

func TestRunnerRestartOnFailure(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	marker := filepath.Join(t.TempDir(), "marker")

	service := &unit.ServiceUnit{
		ExecStart:             helperCommand("failonce", marker),
		Restart:               unit.RestartOnFailure,
		RestartSec:            10 * time.Millisecond,
		StartLimitBurst:       5,
		StartLimitIntervalSec: time.Second,
	}
	runner, collector := newIntegratedRunner(t, "app.service", service)
	changes, _, done := runSCMHandler(t, runner.handler)

	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	states := collectStates(collector)
	var restarts int
	for _, event := range states {
		if event.State == StateRestarting {
			restarts++
		}
	}
	if restarts != 1 {
		t.Errorf("restart events = %d", restarts)
	}
	last := states[len(states)-1]
	if last.State != StateInactive {
		t.Errorf("last state = %q", last.State)
	}
	if last.RestartCount != 1 {
		t.Errorf("restart_count = %d", last.RestartCount)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file: %v", err)
	}
}

func TestRunnerStartLimitExhaustion(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	attempts := filepath.Join(t.TempDir(), "attempts.txt")

	service := &unit.ServiceUnit{
		ExecStart:             helperCommand("record", attempts, "attempt", "1"),
		Restart:               unit.RestartAlways,
		RestartSec:            time.Millisecond,
		StartLimitBurst:       2,
		StartLimitIntervalSec: time.Second,
	}
	runner, collector := newIntegratedRunner(t, "app.service", service)
	changes, _, done := runSCMHandler(t, runner.handler)

	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	states := collectStates(collector)
	last := states[len(states)-1]
	if last.State != StateFailed {
		t.Errorf("last state = %q", last.State)
	}
	if last.RestartCount != 1 {
		t.Errorf("restart_count = %d", last.RestartCount)
	}

	data, err := os.ReadFile(attempts)
	if err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}
