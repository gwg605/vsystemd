package runner

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/gwg605/vsystemd/internal/unit"
)

type ProcessManager struct {
	service *unit.ServiceUnit

	mu       sync.Mutex
	cmd      *exec.Cmd
	streamer *LogStreamer
}

func NewProcessManager(service *unit.ServiceUnit) *ProcessManager {
	return &ProcessManager{service: service}
}

func (p *ProcessManager) SetLogStreamer(streamer *LogStreamer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.streamer = streamer
}

func (p *ProcessManager) Start() error {
	env, err := buildEnvironment(p.service)
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
		if err := runCommand(cmd, env, p.service.WorkingDirectory, stdout, stderr); err != nil && !cmd.ignoreFailure {
			return fmt.Errorf("ExecStartPre failed: %w", err)
		}
	}

	cmd := exec.Command(start.path, start.args...)
	cmd.Dir = p.service.WorkingDirectory
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ExecStart: %w", err)
	}

	p.mu.Lock()
	p.cmd = cmd
	p.mu.Unlock()

	for _, cmd := range post {
		if err := runCommand(cmd, env, p.service.WorkingDirectory, stdout, stderr); err != nil && !cmd.ignoreFailure {
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

	err := cmd.Wait()

	p.mu.Lock()
	if p.cmd == cmd {
		p.cmd = nil
	}
	p.mu.Unlock()

	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
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

func (p *ProcessManager) terminate() {
	p.mu.Lock()
	cmd := p.cmd
	p.cmd = nil
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
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

func buildEnvironment(service *unit.ServiceUnit) ([]string, error) {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok {
			env[key] = value
		}
	}

	for _, file := range service.EnvironmentFiles {
		optional := strings.HasPrefix(file, "-")
		path := strings.TrimPrefix(file, "-")
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
