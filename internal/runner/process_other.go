//go:build !windows

package runner

import (
	"errors"
	"os/exec"
)

func configureCommand(*exec.Cmd) {}

func sendCtrlBreak(int) error {
	return errors.New("console interrupts are not supported")
}
