package runner

import (
	"io"
	"os/exec"
	"sync"
)

type opRole int

const (
	rolePre opRole = iota
	roleMain
	rolePost
	roleStop
)

type cancelMode int

const (
	cancelInterrupt cancelMode = iota
	cancelKill
)

type opResult struct {
	role      opRole
	code      int
	err       error
	cancelled bool
}

type operation interface {
	start() error
	cancel(cancelMode)
}

type pidProvider interface {
	pid() uint32
}

type processOperation struct {
	role        opRole
	cmd         *exec.Cmd
	proc        processHandle
	stdin       io.WriteCloser
	interruptib bool
	processID   uint32
	report      func(opResult)
	mu          sync.Mutex
}

func newProcessOperation(role opRole, cmd *exec.Cmd, report func(opResult)) *processOperation {
	return &processOperation{role: role, cmd: cmd, report: report}
}

func (o *processOperation) start() error { return nil }

func (o *processOperation) cancel(mode cancelMode) {}

func (o *processOperation) pid() uint32 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.processID
}

type sequenceOperation struct {
	role     opRole
	commands []command
	env      []string
	dir      string
	stdout   io.Writer
	stderr   io.Writer
	report   func(opResult)
	current  *processOperation
	mu       sync.Mutex
}

func newSequenceOperation(role opRole, commands []command, env []string, dir string, stdout, stderr io.Writer, report func(opResult)) *sequenceOperation {
	return &sequenceOperation{
		role:     role,
		commands: commands,
		env:      env,
		dir:      dir,
		stdout:   stdout,
		stderr:   stderr,
		report:   report,
	}
}

func (o *sequenceOperation) start() error { return nil }

func (o *sequenceOperation) cancel(mode cancelMode) {}
