package runner

import (
	"errors"
	"io"
	"log/slog"
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

var errOperationCancelled = errors.New("operation cancelled")

type processOperation struct {
	role        opRole
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	interruptib bool
	processID   uint32
	report      func(opResult)

	mu        sync.Mutex
	once      sync.Once
	running   bool
	cancelled bool
}

func newProcessOperation(role opRole, cmd *exec.Cmd, report func(opResult)) *processOperation {
	return &processOperation{role: role, cmd: cmd, report: report}
}

func (o *processOperation) start() error {
	stdin, err := o.cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := o.cmd.Start(); err != nil {
		stdin.Close()
		return err
	}

	pid := o.cmd.Process.Pid

	o.mu.Lock()
	o.stdin = stdin
	o.processID = uint32(pid)
	o.running = true
	o.mu.Unlock()

	go o.wait()
	return nil
}

func (o *processOperation) wait() {
	err := o.cmd.Wait()

	o.mu.Lock()
	o.running = false
	stdin := o.stdin
	o.stdin = nil
	o.mu.Unlock()

	if stdin != nil {
		stdin.Close()
	}

	code, waitErr := processExitCode(err)
	o.finish(opResult{role: o.role, code: code, err: waitErr, cancelled: o.isCancelled()})
}

func (o *processOperation) cancel(mode cancelMode) {
	o.mu.Lock()
	if !o.running {
		o.mu.Unlock()
		return
	}
	o.cancelled = true
	cmd := o.cmd
	pid := int(o.processID)
	stdin := o.stdin
	interruptible := o.interruptib
	o.mu.Unlock()

	if mode == cancelInterrupt && interruptible {
		if err := sendConsoleInterrupt(pid); err != nil {
			slog.Warn("runner: console interrupt failed", "pid", pid, "error", err)
		}
		if stdin != nil {
			if err := writeStopConfirmation(stdin); err != nil {
				slog.Warn("runner: write stop confirmation failed", "pid", pid, "error", err)
			}
		}
		return
	}

	if cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil {
			slog.Warn("runner: kill failed", "pid", pid, "error", err)
		}
	}
}

func (o *processOperation) finish(result opResult) {
	o.once.Do(func() { o.report(result) })
}

func (o *processOperation) isCancelled() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cancelled
}

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

	mu        sync.Mutex
	once      sync.Once
	current   *processOperation
	cancelled bool
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

func (o *sequenceOperation) start() error {
	if len(o.commands) == 0 {
		o.finish(opResult{role: o.role})
		return nil
	}
	return o.spawn(0)
}

func (o *sequenceOperation) spawn(index int) error {
	spec := o.commands[index]
	cmd := exec.Command(spec.path, spec.args...)
	cmd.Dir = o.dir
	cmd.Env = o.env
	cmd.Stdout = o.stdout
	cmd.Stderr = o.stderr
	configureCommand(cmd)

	op := newProcessOperation(o.role, cmd, func(result opResult) {
		o.onCommandDone(index, spec.ignoreFailure, result)
	})

	o.mu.Lock()
	if o.cancelled {
		o.mu.Unlock()
		return errOperationCancelled
	}
	o.current = op
	o.mu.Unlock()

	if err := op.start(); err != nil {
		o.mu.Lock()
		o.current = nil
		o.mu.Unlock()
		return err
	}
	return nil
}

func (o *sequenceOperation) onCommandDone(index int, ignoreFailure bool, result opResult) {
	o.mu.Lock()
	o.current = nil
	o.mu.Unlock()

	if result.cancelled {
		o.finish(result)
		return
	}
	if result.err != nil {
		o.finish(result)
		return
	}
	if result.code != 0 && !ignoreFailure {
		o.finish(result)
		return
	}

	next := index + 1
	if next >= len(o.commands) {
		o.finish(opResult{role: o.role})
		return
	}
	if err := o.spawn(next); err != nil {
		if errors.Is(err, errOperationCancelled) {
			o.finish(opResult{role: o.role, cancelled: true})
			return
		}
		o.finish(opResult{role: o.role, code: -1, err: err})
	}
}

func (o *sequenceOperation) cancel(mode cancelMode) {
	o.mu.Lock()
	if o.cancelled {
		o.mu.Unlock()
		return
	}
	o.cancelled = true
	current := o.current
	o.mu.Unlock()

	if current != nil {
		current.cancel(mode)
		return
	}
	o.finish(opResult{role: o.role, cancelled: true})
}

func (o *sequenceOperation) finish(result opResult) {
	o.once.Do(func() { o.report(result) })
}

func processExitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return -1, err
}

func writeStopConfirmation(stdin io.Writer) error {
	_, err := stdin.Write([]byte("Y\r\n"))
	return err
}
