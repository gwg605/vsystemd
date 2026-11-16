package runner

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/unit"
)

type ProcessManager struct {
	service *unit.ServiceUnit
	env     EnvironmentDefaults

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	streamer *LogStreamer
	proc     processHandle
}

// EnvironmentDefaults describes how the clean environment for service
// processes is built: Inherit lists variables taken from the runner's
// environment, Vars are extra variables whose values may reference the runner's
// environment via %NAME% placeholders.
type EnvironmentDefaults struct {
	Inherit []string
	Vars    map[string]string
}

func NewProcessManager(service *unit.ServiceUnit, env EnvironmentDefaults) *ProcessManager {
	return &ProcessManager{service: service, env: env}
}

func (p *ProcessManager) SetLogStreamer(streamer *LogStreamer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.streamer = streamer
}

func (p *ProcessManager) Start() error {
	env, err := buildEnvironment(p.service, p.env)
	if err != nil {
		return err
	}

	pre, err := parseCommands(p.service.ExecStartPre)
	if err != nil {
		return fmt.Errorf("parse ExecStartPre: %w", err)
	}
	start, err := parseCommand(p.service.ExecStart)
	if err != nil {
		return fmt.Errorf("parse ExecStart: %w", err)
	}
	if start == nil {
		return errors.New("ExecStart is required")
	}
	post, err := parseCommands(p.service.ExecStartPost)
	if err != nil {
		return fmt.Errorf("parse ExecStartPost: %w", err)
	}

	p.mu.Lock()
	streamer := p.streamer
	p.mu.Unlock()
	var stdout, stderr io.Writer
	if streamer != nil {
		stdout = streamer.Stdout()
		stderr = streamer.Stderr()
	}

	for _, cmd := range pre {
		slog.Debug("process: running ExecStartPre", "command", cmd.path, "args", cmd.args)
		if err := runCommand(cmd, env, p.service.WorkingDirectory, stdout, stderr); err != nil && !cmd.ignoreFailure {
			slog.Error("process: ExecStartPre failed", "command", cmd.path, "args", cmd.args, "error", err)
			return fmt.Errorf("ExecStartPre failed: %w", err)
		}
	}

	cmd := exec.Command(start.path, start.args...)
	cmd.Dir = p.service.WorkingDirectory
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	configureCommand(cmd)
	slog.Info("process: starting ExecStart",
		"path", start.path,
		"args", start.args,
		"working_directory", p.service.WorkingDirectory)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		slog.Error("process: create ExecStart stdin failed", "path", start.path, "error", err)
		return fmt.Errorf("create ExecStart stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		slog.Error("process: ExecStart start failed", "path", start.path, "error", err)
		return fmt.Errorf("start ExecStart: %w", err)
	}

	p.mu.Lock()
	p.cmd = cmd
	p.stdin = stdin
	proc, err := openProcessHandle(cmd.Process.Pid)
	if err != nil {
		slog.Warn("process: open process handle failed", "pid", cmd.Process.Pid, "error", err)
	}
	p.proc = proc
	p.mu.Unlock()
	slog.Info("process: ExecStart started", "pid", cmd.Process.Pid)

	for _, cmd := range post {
		slog.Debug("process: running ExecStartPost", "command", cmd.path, "args", cmd.args)
		if err := runCommand(cmd, env, p.service.WorkingDirectory, stdout, stderr); err != nil && !cmd.ignoreFailure {
			slog.Error("process: ExecStartPost failed", "command", cmd.path, "args", cmd.args, "error", err)
			p.terminate()
			return fmt.Errorf("ExecStartPost failed: %w", err)
		}
	}
	return nil
}

func (p *ProcessManager) Wait() (int, error) {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil {
		return 0, errors.New("process is not running")
	}

	pid := uint32(cmd.Process.Pid)
	err := cmd.Wait()

	p.mu.Lock()
	if p.cmd == cmd {
		p.cmd = nil
		if p.stdin != nil {
			p.stdin.Close()
			p.stdin = nil
		}
		p.proc.close()
	}
	p.mu.Unlock()

	if err == nil {
		slog.Info("process: process exited", "pid", pid, "exit_code", 0)
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		slog.Info("process: process exited", "pid", pid, "exit_code", exitErr.ExitCode())
		return exitErr.ExitCode(), nil
	}
	slog.Error("process: process wait failed", "pid", pid, "error", err)
	return -1, err
}

func (p *ProcessManager) PID() uint32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return uint32(p.cmd.Process.Pid)
}

func (p *ProcessManager) Interrupt() error {
	p.mu.Lock()
	if p.cmd == nil || p.cmd.Process == nil {
		p.mu.Unlock()
		return errors.New("process is not running")
	}
	pid := p.cmd.Process.Pid
	if !p.proc.alive() {
		p.mu.Unlock()
		return fmt.Errorf("process %d is not running", pid)
	}

	slog.Info("process: sending console ctrl-c", "pid", pid)
	err := sendConsoleInterrupt(pid)
	stdin := p.stdin
	p.mu.Unlock()

	if err != nil {
		slog.Warn("process: console ctrl-c failed", "pid", pid, "error", err)
		return err
	}

	if stdin == nil {
		slog.Warn("process: child stdin is not available", "pid", pid)
		return nil
	}
	if err := writeStopConfirmation(stdin); err != nil {
		slog.Warn("process: write stop confirmation to child stdin failed", "pid", pid, "error", err)
		return err
	}
	slog.Info("process: sent stop confirmation to child stdin", "pid", pid)
	return nil
}

func writeStopConfirmation(stdin io.Writer) error {
	_, err := stdin.Write([]byte("Y\r\n"))
	return err
}

func (p *ProcessManager) Kill() {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	slog.Warn("process: killing process", "pid", cmd.Process.Pid)
	if err := cmd.Process.Kill(); err != nil {
		slog.Warn("process: kill failed", "pid", cmd.Process.Pid, "error", err)
	}
}

func (p *ProcessManager) logWriters() (io.Writer, io.Writer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.streamer == nil {
		return nil, nil
	}
	return p.streamer.Stdout(), p.streamer.Stderr()
}

func (p *ProcessManager) terminate() {
	p.mu.Lock()
	cmd := p.cmd
	stdin := p.stdin
	p.cmd = nil
	p.stdin = nil
	p.proc.close()
	p.mu.Unlock()

	if stdin != nil {
		stdin.Close()
	}
	if cmd == nil || cmd.Process == nil {
		return
	}
	slog.Warn("process: terminating process", "pid", cmd.Process.Pid)
	cmd.Process.Kill()
	cmd.Wait()
}

func runCommand(cmd command, env []string, dir string, stdout, stderr io.Writer) error {
	c := exec.Command(cmd.path, cmd.args...)
	c.Dir = dir
	c.Env = env
	c.Stdout = stdout
	c.Stderr = stderr
	return c.Run()
}

func buildEnvironment(service *unit.ServiceUnit, defaults EnvironmentDefaults) ([]string, error) {
	parent := parentEnvironment()

	env := make(map[string]string)
	for _, name := range defaults.Inherit {
		if value, ok := parent[strings.ToLower(name)]; ok {
			env[name] = value
		}
	}
	for key, value := range defaults.Vars {
		env[key] = expandEnv(value, parent)
	}

	for _, file := range service.EnvironmentFiles {
		optional := strings.HasPrefix(file, "-")
		path := strings.TrimPrefix(file, "-")
		if err := logwriter.ValidateUnitPath(path); err != nil {
			return nil, fmt.Errorf("invalid EnvironmentFile: %w", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if optional && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read EnvironmentFile %s: %w", path, err)
		}
		for lineNo, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			key = strings.TrimSpace(key)
			if !ok || key == "" {
				return nil, fmt.Errorf("%s:%d: invalid environment line", path, lineNo+1)
			}
			env[key] = strings.TrimSpace(value)
		}
	}

	for key, value := range service.Environment {
		env[key] = value
	}

	result := make([]string, 0, len(env))
	for key, value := range env {
		result = append(result, key+"="+value)
	}
	return result, nil
}

// parentEnvironment returns the runner's environment keyed case-insensitively.
func parentEnvironment() map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok {
			env[strings.ToLower(key)] = value
		}
	}
	return env
}

// expandEnv substitutes %NAME% placeholders from lookup (case-insensitive).
// "%%" yields a literal "%", an undefined name yields an empty string, and a
// "%" without a closing "%" is left as-is with a warning.
func expandEnv(value string, lookup map[string]string) string {
	var b strings.Builder
	for i := 0; i < len(value); {
		if value[i] != '%' {
			b.WriteByte(value[i])
			i++
			continue
		}
		if i+1 < len(value) && value[i+1] == '%' {
			b.WriteByte('%')
			i += 2
			continue
		}
		end := strings.IndexByte(value[i+1:], '%')
		if end < 0 {
			slog.Warn("runner: malformed environment placeholder", "value", value)
			b.WriteString(value[i:])
			break
		}
		name := value[i+1 : i+1+end]
		b.WriteString(lookup[strings.ToLower(name)])
		i += end + 2
	}
	return b.String()
}
