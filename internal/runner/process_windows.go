//go:build windows

package runner

import (
	"fmt"
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
	attached, _, attachErr := procAttachConsole.Call(uintptr(pid))
	if attached == 0 {
		return fmt.Errorf("attach console %d: %w", pid, attachErr)
	}
	defer procFreeConsole.Call()

	ignored, _, handlerErr := procSetConsoleCtrlHandler.Call(0, 1)
	if ignored == 0 {
		return fmt.Errorf("ignore ctrl-c: %w", handlerErr)
	}
	defer procSetConsoleCtrlHandler.Call(0, 0)

	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0); err != nil {
		return fmt.Errorf("generate ctrl-c: %w", err)
	}
	return nil
}
