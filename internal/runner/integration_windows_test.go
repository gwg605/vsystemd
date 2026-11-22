//go:build windows

package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/storage"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
	"github.com/gwg605/vsystemd/internal/transport"
	transportfake "github.com/gwg605/vsystemd/internal/transport/fake"
	"github.com/gwg605/vsystemd/internal/unit"
)

type staticContent struct{ content unit.Content }

func (s staticContent) LoadUnitContent(string) (unit.Content, error) { return s.content, nil }

func newIntegratedRunner(t *testing.T, name, unitText string) (*Runner, *stateCollector) {
	t.Helper()
	return newIntegratedRunnerConfig(t, name, unitText, config.Config{LogsDir: t.TempDir()})
}

func newIntegratedRunnerConfig(t *testing.T, name, unitText string, cfg config.Config) (*Runner, *stateCollector) {
	t.Helper()

	network := transportfake.NewNetwork()
	collector := newStateCollector()
	daemon := network.New(transport.DefaultConfig(), transport.NewPeer(transport.PeerDaemon, daemonPeer, false), collector.handler)
	t.Cleanup(func() { daemon.Close() })

	store := storagefake.New()
	store.WriteUnit(name, []byte(unitText))
	storeFactory := func() (storage.Storage, error) { return store, nil }

	factory := func(local transport.Peer, handler transport.MsgHandler) (transport.Transport, error) {
		return network.New(transport.DefaultConfig(), local, handler), nil
	}
	content := unit.Content(unitText)
	runner, err := NewRunner(name, content.Hash(), staticContent{content}, storeFactory, factory, cfg, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	t.Cleanup(func() { runner.Close() })

	return runner, collector
}

func runControlHandler(t *testing.T, handler *controlHandler) (<-chan svc.Status, chan<- svc.ChangeRequest, <-chan struct{}) {
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
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not return")
	}
}

func collectStates(collector *stateCollector) []protocol.StateEvent {
	close(collector.events)
	var states []protocol.StateEvent
	for event := range collector.events {
		states = append(states, event)
	}
	return states
}

func TestRunnerStartStop(t *testing.T) {
	dir := t.TempDir()
	stopFile := filepath.Join(dir, "stop.txt")

	unitText := fmt.Sprintf("[Service]\nExecStart=%s\nExecStop=%s\nTimeoutStopSec=2s\n",
		helperCommand("sleep", "5s"), helperCommand("record", stopFile, "stopped"))
	runner, collector := newIntegratedRunner(t, "app.service", unitText)
	changes, requests, done := runControlHandler(t, runner.handler)

	waitForStatus(t, changes, svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	states := collectStates(collector)
	if len(states) < 3 {
		t.Fatalf("states = %v", states)
	}
	if states[0].State != protocol.StateActivating || states[1].State != protocol.StateActive {
		t.Errorf("states = %v", states)
	}
	if states[len(states)-1].State != protocol.StateInactive {
		t.Errorf("last state = %q", states[len(states)-1].State)
	}
	if data := waitForFile(t, stopFile); string(data) != "stopped\n" {
		t.Errorf("ExecStop output = %q", data)
	}
}

func TestRunnerRestartOnFailure(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")

	unitText := fmt.Sprintf("[Service]\nExecStart=%s\nRestart=on-failure\nRestartSec=10ms\nStartLimitBurst=5\nStartLimitIntervalSec=1s\n",
		helperCommand("failonce", marker))
	runner, collector := newIntegratedRunner(t, "app.service", unitText)
	changes, _, done := runControlHandler(t, runner.handler)

	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	states := collectStates(collector)
	var restarts int
	for _, event := range states {
		if event.State == protocol.StateRestarting {
			restarts++
		}
	}
	if restarts != 1 {
		t.Errorf("restart events = %d", restarts)
	}
	last := states[len(states)-1]
	if last.State != protocol.StateInactive {
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
	attempts := filepath.Join(t.TempDir(), "attempts.txt")

	unitText := fmt.Sprintf("[Service]\nExecStart=%s\nRestart=always\nRestartSec=1ms\nStartLimitBurst=2\nStartLimitIntervalSec=1s\n",
		helperCommand("record", attempts, "attempt", "1"))
	runner, collector := newIntegratedRunner(t, "app.service", unitText)
	changes, _, done := runControlHandler(t, runner.handler)

	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	states := collectStates(collector)
	last := states[len(states)-1]
	if last.State != protocol.StateFailed {
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

func TestRunnerLogsDirectory(t *testing.T) {
	logsRoot := t.TempDir()
	envFile := filepath.Join(t.TempDir(), "env.txt")

	unitText := fmt.Sprintf("[Service]\nLogsDirectory=app\nStandardOutput=append:%%L\\app\\out.log\nExecStart=%s\n",
		helperCommand("recordenv", envFile, "LOGS_DIRECTORY"))
	runner, _ := newIntegratedRunnerConfig(t, "app.service", unitText, config.Config{LogsDir: logsRoot})
	changes, _, done := runControlHandler(t, runner.handler)

	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	dir := filepath.Join(logsRoot, "app")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("LogsDirectory was not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.log")); err != nil {
		t.Errorf("StandardOutput file was not created: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if string(data) != dir {
		t.Errorf("LOGS_DIRECTORY = %q, want %q", data, dir)
	}
}

func TestRunnerRuntimeDirectory(t *testing.T) {
	runRoot := t.TempDir()
	envFile := filepath.Join(t.TempDir(), "env.txt")

	unitText := fmt.Sprintf("[Service]\nRuntimeDirectory=app\nExecStart=%s\n",
		helperCommand("recordenv", envFile, "RUNTIME_DIRECTORY"))
	runner, _ := newIntegratedRunnerConfig(t, "app.service", unitText, config.Config{LogsDir: t.TempDir(), RunDir: runRoot})

	dir := filepath.Join(runRoot, "app")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("RuntimeDirectory was not created: %v", err)
	}

	changes, _, done := runControlHandler(t, runner.handler)
	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("RuntimeDirectory was not removed on stop: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if string(data) != dir {
		t.Errorf("RUNTIME_DIRECTORY = %q, want %q", data, dir)
	}
}

func TestRunnerSnapshotErrors(t *testing.T) {
	unitText := "[Service]\nExecStart=x\n"
	content := unit.Content(unitText)
	store := storagefake.New()
	store.WriteUnit("app.service", []byte(unitText))
	storeFactory := func() (storage.Storage, error) { return store, nil }
	network := transportfake.NewNetwork()
	factory := func(local transport.Peer, handler transport.MsgHandler) (transport.Transport, error) {
		return network.New(transport.DefaultConfig(), local, handler), nil
	}
	cfg := config.Config{LogsDir: t.TempDir()}

	if _, err := NewRunner("app.service", "", staticContent{content}, storeFactory, factory, cfg, time.Now().UTC()); err == nil {
		t.Error("missing snapshot hash: error = nil")
	}
	if _, err := NewRunner("app.service", "deadbeef", staticContent{content}, storeFactory, factory, cfg, time.Now().UTC()); err == nil {
		t.Error("snapshot hash mismatch: error = nil")
	}
}

func TestRunnerReportsLoadedHash(t *testing.T) {
	unitText := fmt.Sprintf("[Service]\nExecStart=%s\n",
		helperCommand("record", filepath.Join(t.TempDir(), "done.txt"), "done"))
	runner, collector := newIntegratedRunner(t, "app.service", unitText)
	changes, _, done := runControlHandler(t, runner.handler)

	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	states := collectStates(collector)
	last := states[len(states)-1]
	if want := unit.Content(unitText).Hash(); last.LoadedHash != want {
		t.Errorf("LoadedHash = %q, want %q", last.LoadedHash, want)
	}
}
