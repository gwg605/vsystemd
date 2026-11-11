//go:build windows

package runner

import (
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
