//go:build !windows

package runner

import (
	"errors"
	"os/exec"
)

func configureCommand(*exec.Cmd) {}

type processHandle struct{}

func openProcessHandle(int) (processHandle, error) {
	return processHandle{}, nil
}

func (*processHandle) close() {}

func (processHandle) alive() bool {
	return true
}

func sendConsoleInterrupt(int) error {
	return errors.New("console interrupts are not supported")
}
