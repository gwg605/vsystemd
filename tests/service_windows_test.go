//go:build windows

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/daemon"
)

func TestServiceLifecycle(t *testing.T) {
	env := newTestEnv()

	stopFile := filepath.Join(t.TempDir(), "stop.txt")
	unitText := fmt.Sprintf("[Service]\nExecStart=%s\nExecStop=%s\nTimeoutStopSec=200ms\n",
		helperCommand("sleep", "5s"), helperCommand("record", stopFile, "stopped"))
	env.store.WriteUnit("app.service", []byte(unitText))

	d := env.newDaemon(t)
	if err := d.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	r := env.newRunner(t, "app.service")
	requests, changes, done := startRunner(t, r)

	waitForStatus(t, changes, svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	if data := waitForFile(t, stopFile); string(data) != "stopped\n" {
		t.Errorf("ExecStop output = %q", data)
	}
	state := waitForState(t, d, "app.service", daemon.RunnerStateInactive)
	if state.Unit != "app.service" {
		t.Errorf("state unit = %q", state.Unit)
	}
}

func TestRestartOnFailure(t *testing.T) {
	env := newTestEnv()
	marker := filepath.Join(t.TempDir(), "marker")
	unitText := fmt.Sprintf("[Service]\nExecStart=%s\nRestart=on-failure\nRestartSec=10ms\nStartLimitBurst=5\nStartLimitIntervalSec=1s\n",
		helperCommand("failonce", marker))
	env.store.WriteUnit("app.service", []byte(unitText))

	d := env.newDaemon(t)
	if err := d.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	r := env.newRunner(t, "app.service")
	_, changes, done := startRunner(t, r)

	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	state := waitForState(t, d, "app.service", daemon.RunnerStateInactive)
	if state.RestartCount != 1 {
		t.Errorf("restart_count = %d", state.RestartCount)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file: %v", err)
	}
}

func TestStartLimitExhaustion(t *testing.T) {
	env := newTestEnv()
	attempts := filepath.Join(t.TempDir(), "attempts.txt")
	unitText := fmt.Sprintf("[Service]\nExecStart=%s\nRestart=always\nRestartSec=1ms\nStartLimitBurst=2\nStartLimitIntervalSec=1s\n",
		helperCommand("record", attempts, "attempt", "1"))
	env.store.WriteUnit("app.service", []byte(unitText))

	d := env.newDaemon(t)
	if err := d.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	r := env.newRunner(t, "app.service")
	_, changes, done := startRunner(t, r)

	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	state := waitForState(t, d, "app.service", daemon.RunnerStateFailed)
	if state.RestartCount != 1 {
		t.Errorf("restart_count = %d", state.RestartCount)
	}
	data, err := os.ReadFile(attempts)
	if err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}
