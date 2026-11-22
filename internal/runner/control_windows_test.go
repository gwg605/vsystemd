//go:build windows

package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/env"
	"github.com/gwg605/vsystemd/internal/unit"
)

func waitForStatus(t *testing.T, changes <-chan svc.Status, state svc.State) {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case status := <-changes:
			if status.State == state {
				return
			}
		case <-timeout.C:
			t.Fatalf("state %v was not reported", state)
		}
	}
}

func waitForFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("read %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newTestHandler(service *unit.ServiceUnit) *controlHandler {
	return newControlHandler(service, newRestartController(service), env.Defaults{}, nil, "")
}

func TestControlHandlerStop(t *testing.T) {
	stopFile := filepath.Join(t.TempDir(), "stop.txt")

	service := &unit.ServiceUnit{
		ExecStart:      helperCommand("sleep", "5s"),
		ExecStop:       helperCommand("record", stopFile, "stopped"),
		TimeoutStopSec: 2 * time.Second,
	}
	changes, requests, done := runControlHandler(t, newTestHandler(service))

	waitForStatus(t, changes, svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	if data := waitForFile(t, stopFile); string(data) != "stopped\n" {
		t.Errorf("ExecStop output = %q", data)
	}
}

func TestControlHandlerInterrogate(t *testing.T) {
	service := &unit.ServiceUnit{
		ExecStart:      helperCommand("sleep", "5s"),
		TimeoutStopSec: 100 * time.Millisecond,
	}
	changes, requests, done := runControlHandler(t, newTestHandler(service))

	waitForStatus(t, changes, svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Interrogate}
	waitForStatus(t, changes, svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)
}

func TestControlHandlerExitWithoutRestart(t *testing.T) {
	exitFile := filepath.Join(t.TempDir(), "exit.txt")

	service := &unit.ServiceUnit{
		ExecStart: helperCommand("record", exitFile, "done", "3"),
	}
	changes, _, done := runControlHandler(t, newTestHandler(service))

	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	if data := waitForFile(t, exitFile); string(data) != "done\n" {
		t.Errorf("output = %q", data)
	}
}

func TestControlHandlerExecStartPreFailure(t *testing.T) {
	orderFile := filepath.Join(t.TempDir(), "order.txt")

	service := &unit.ServiceUnit{
		ExecStartPre: []string{helperCommand("record", orderFile, "pre", "1")},
		ExecStart:    helperCommand("record", orderFile, "start"),
	}
	changes, _, done := runControlHandler(t, newTestHandler(service))

	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)

	if data := waitForFile(t, orderFile); string(data) != "pre\n" {
		t.Errorf("order = %q", data)
	}
}

func TestControlHandlerMissingExecStart(t *testing.T) {
	changes, _, done := runControlHandler(t, newTestHandler(&unit.ServiceUnit{}))

	waitForStatus(t, changes, svc.Stopped)
	waitForDone(t, done)
}
