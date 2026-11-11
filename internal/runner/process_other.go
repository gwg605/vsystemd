//go:build !windows

package runner

import (
	"errors"
	"os/exec"
)

func configureCommand(*exec.Cmd) {}

func sendConsoleInterrupt(int) error {
	return errors.New("console interrupts are not supported")
}
