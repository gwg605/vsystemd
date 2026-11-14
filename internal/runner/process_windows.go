//go:build windows

package runner

import (
	"fmt"
	"log/slog"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procAttachConsole         = kernel32.NewProc("AttachConsole")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
	procSetConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
)

func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_CONSOLE,
		HideWindow:    true,
	}
}

func sendConsoleInterrupt(pid int) error {
	slog.Info("process: attaching to child console", "pid", pid)
	attached, _, attachErr := procAttachConsole.Call(uintptr(pid))
	if attached == 0 {
		slog.Warn("process: AttachConsole failed", "pid", pid, "error", attachErr)
		return fmt.Errorf("attach console %d: %w", pid, attachErr)
	}
	slog.Debug("process: attached to child console", "pid", pid)

	defer func() {
		if freed, _, freeErr := procFreeConsole.Call(); freed == 0 {
			slog.Warn("process: FreeConsole failed", "pid", pid, "error", freeErr)
		}
	}()

	ignored, _, handlerErr := procSetConsoleCtrlHandler.Call(0, 1)
	if ignored == 0 {
		slog.Warn("process: SetConsoleCtrlHandler(TRUE) failed", "pid", pid, "error", handlerErr)
		return fmt.Errorf("ignore ctrl-c: %w", handlerErr)
	}
	slog.Debug("process: ignoring ctrl-c in runner while sending it to child", "pid", pid)

	slog.Info("process: sending CTRL_C_EVENT to child console", "pid", pid)
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0); err != nil {
		slog.Warn("process: GenerateConsoleCtrlEvent failed", "pid", pid, "error", err)
		return fmt.Errorf("generate ctrl-c: %w", err)
	}
	slog.Info("process: CTRL_C_EVENT sent", "pid", pid)
	return nil
}
