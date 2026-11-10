//go:build windows

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/daemon"
	"github.com/gwg605/vsystemd/internal/unit"
)

func TestServiceLifecycle(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	env := newTestEnv()
	env.store.WriteUnit("app.service", []byte("[Service]\nExecStart=app\n"))

	stopFile := filepath.Join(t.TempDir(), "stop.txt")
	service := &unit.ServiceUnit{
		ExecStart:      helperCommand("sleep", "5s"),
		ExecStop:       helperCommand("record", stopFile, "stopped"),
		TimeoutStopSec: 200 * time.Millisecond,
	}

	d := env.newDaemon(t)
	if err := d.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	r := env.newRunner(t, "app.service", service)
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
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	env := newTestEnv()
	env.store.WriteUnit("app.service", []byte("[Service]\nExecStart=app\n"))
	marker := filepath.Join(t.TempDir(), "marker")

	service := &unit.ServiceUnit{
		ExecStart:             helperCommand("failonce", marker),
		Restart:               unit.RestartOnFailure,
		RestartSec:            10 * time.Millisecond,
		StartLimitBurst:       5,
		StartLimitIntervalSec: time.Second,
	}

	d := env.newDaemon(t)
	if err := d.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	r := env.newRunner(t, "app.service", service)
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
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	env := newTestEnv()
	env.store.WriteUnit("app.service", []byte("[Service]\nExecStart=app\n"))
	attempts := filepath.Join(t.TempDir(), "attempts.txt")

	service := &unit.ServiceUnit{
		ExecStart:             helperCommand("record", attempts, "attempt", "1"),
		Restart:               unit.RestartAlways,
		RestartSec:            time.Millisecond,
		StartLimitBurst:       2,
		StartLimitIntervalSec: time.Second,
	}

	d := env.newDaemon(t)
	if err := d.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	r := env.newRunner(t, "app.service", service)
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
