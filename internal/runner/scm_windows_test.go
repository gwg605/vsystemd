//go:build windows

package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/unit"
)

func waitForStatus(t *testing.T, changes <-chan svc.Status, state svc.State) {
	t.Helper()
	timeout := time.NewTimer(2 * time.Second)
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

func TestSCMHandlerStop(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	dir := t.TempDir()
	stopFile := filepath.Join(dir, "stop.txt")

	service := &unit.ServiceUnit{
		ExecStart:      helperCommand("sleep", "5s"),
		ExecStop:       helperCommand("record", stopFile, "stopped"),
		TimeoutStopSec: 100 * time.Millisecond,
	}
	process := NewProcessManager(service)
	handler := NewSCMHandler(service, process, NewRestartController(service))

	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 20)
	done := make(chan struct{})
	go func() {
		handler.Execute(nil, requests, changes)
		close(done)
	}()

	waitForStatus(t, changes, svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	waitForStatus(t, changes, svc.Stopped)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not return")
	}

	if data := waitForFile(t, stopFile); string(data) != "stopped\n" {
		t.Errorf("ExecStop output = %q", data)
	}
}

func TestSCMHandlerInterrogate(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	service := &unit.ServiceUnit{
		ExecStart:      helperCommand("sleep", "5s"),
		TimeoutStopSec: 100 * time.Millisecond,
	}
	process := NewProcessManager(service)
	handler := NewSCMHandler(service, process, NewRestartController(service))

	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 20)
	done := make(chan struct{})
	go func() {
		handler.Execute(nil, requests, changes)
		close(done)
	}()

	waitForStatus(t, changes, svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Interrogate}
	waitForStatus(t, changes, svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	waitForStatus(t, changes, svc.Stopped)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not return")
	}
}

func TestSCMHandlerExitWithoutRestart(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	dir := t.TempDir()
	exitFile := filepath.Join(dir, "exit.txt")

	service := &unit.ServiceUnit{
		ExecStart: helperCommand("record", exitFile, "done", "3"),
	}
	process := NewProcessManager(service)
	handler := NewSCMHandler(service, process, NewRestartController(service))

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

	if data := waitForFile(t, exitFile); string(data) != "done\n" {
		t.Errorf("output = %q", data)
	}
}
