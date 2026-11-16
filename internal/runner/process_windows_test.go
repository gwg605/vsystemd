//go:build windows

package runner

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigureCommand(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	configureCommand(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	if cmd.SysProcAttr.CreationFlags != windows.CREATE_NEW_CONSOLE {
		t.Errorf("CreationFlags = %#x", cmd.SysProcAttr.CreationFlags)
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Error("HideWindow = false")
	}
}

func TestProcessHandle(t *testing.T) {
	handle, err := openProcessHandle(os.Getpid())
	if err != nil {
		t.Fatalf("openProcessHandle() error = %v", err)
	}
	if !handle.alive() {
		t.Error("alive() = false for the current process")
	}
	handle.close()

	if _, err := openProcessHandle(0); err == nil {
		t.Error("openProcessHandle(0) error = nil, want error")
	}
}
