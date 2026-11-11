//go:build windows

package runner

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")

	procAllocConsole     = kernel32.NewProc("AllocConsole")
	procGetConsoleCP     = kernel32.NewProc("GetConsoleCP")
	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	procShowWindow       = user32.NewProc("ShowWindow")
)

func configureCommand(cmd *exec.Cmd) {
	ensureConsole()
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}

func ensureConsole() {
	if hasConsole() {
		return
	}

	allocated, _, _ := procAllocConsole.Call()
	if allocated == 0 {
		return
	}

	window, _, _ := procGetConsoleWindow.Call()
	if window != 0 {
		procShowWindow.Call(window, uintptr(windows.SW_HIDE))
	}
}

func hasConsole() bool {
	codepage, _, _ := procGetConsoleCP.Call()
	return codepage != 0
}

func sendCtrlBreak(pid int) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}
