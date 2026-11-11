//go:build windows

package runner

import (
	"log/slog"
	"runtime/debug"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/unit"
)

const defaultStopTimeout = 90 * time.Second

type waitResult struct {
	code int
	err  error
}

type SCMHandler struct {
	service     *unit.ServiceUnit
	process     *ProcessManager
	restart     *RestartController
	reporter    *StateReporter
	stopTimeout time.Duration
}

func NewSCMHandler(service *unit.ServiceUnit, process *ProcessManager, restart *RestartController) *SCMHandler {
	return &SCMHandler{
		service:     service,
		process:     process,
		restart:     restart,
		stopTimeout: resolveStopTimeout(service),
	}
}

func (h *SCMHandler) SetStateReporter(reporter *StateReporter) {
	h.reporter = reporter
}

func Run(name string, handler *SCMHandler) error {
	return svc.Run(name, handler)
}

func resolveStopTimeout(service *unit.ServiceUnit) time.Duration {
	if service.TimeoutStopSec > 0 {
		return service.TimeoutStopSec
	}
	return defaultStopTimeout
}

func (h *SCMHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("runner: panic in service handler",
				"panic", recovered,
				"stack", string(debug.Stack()))
			h.process.Kill()
			changes <- svc.Status{State: svc.Stopped}
			ssec, errno = false, 1
		}
	}()

	slog.Info("runner: service execute started")
	status := svc.Status{State: svc.StartPending}
	changes <- status
	h.restart.Reset()

	lastExit := 0
	lastError := ""
	started := false

outer:
	for {
		if !h.restart.AllowStart() {
			slog.Error("runner: start limit reached", "restart_count", h.restart.RestartCount())
			h.report(StateFailed, 0, lastExit, "start limit exceeded")
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}
		if started {
			h.restart.RecordRestart()
		}
		started = true

		slog.Info("runner: starting process", "restart_count", h.restart.RestartCount())
		h.report(StateActivating, 0, lastExit, lastError)
		if err := h.process.Start(); err != nil {
			lastError = err.Error()
			slog.Error("runner: process start failed", "error", err)
			h.report(StateFailed, 0, lastExit, lastError)
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}

		status = svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
		changes <- status
		slog.Info("runner: process running", "pid", h.process.PID())
		h.report(StateActive, h.process.PID(), 0, "")

		waitCh := make(chan waitResult, 1)
		go func() {
			code, err := h.process.Wait()
			waitCh <- waitResult{code: code, err: err}
		}()

		var delayCh <-chan time.Time
		for {
			select {
			case result := <-waitCh:
				lastExit = result.code
				if result.err != nil {
					lastError = result.err.Error()
				} else {
					lastError = ""
				}
				slog.Info("runner: process wait result", "exit_code", result.code, "error", result.err)

				if !h.restart.ShouldRestart(result.code) {
					if result.code != 0 || result.err != nil {
						slog.Warn("runner: service failed", "exit_code", result.code, "error", lastError)
						h.report(StateFailed, 0, lastExit, lastError)
					} else {
						slog.Info("runner: service exited", "exit_code", result.code)
						h.report(StateInactive, 0, lastExit, "")
					}
					changes <- svc.Status{State: svc.Stopped}
					return false, 0
				}

				delay := h.restart.Delay()
				slog.Warn("runner: scheduling restart", "delay", delay, "exit_code", result.code)
				h.report(StateRestarting, 0, lastExit, lastError)
				if delay > 0 {
					delayCh = time.After(delay)
					waitCh = nil
					continue
				}
				continue outer
			case <-delayCh:
				continue outer
			case request := <-requests:
				switch request.Cmd {
				case svc.Interrogate:
					slog.Debug("runner: interrogate")
					changes <- status
				case svc.Stop, svc.Shutdown:
					slog.Info("runner: stop requested", "control", request.Cmd, "pid", h.process.PID(), "timeout", h.stopTimeout)
					status = svc.Status{State: svc.StopPending}
					changes <- status
					if waitCh == nil {
						slog.Info("runner: stop requested while waiting to restart", "pid", 0, "timeout", h.stopTimeout)
						h.report(StateInactive, 0, lastExit, "")
						changes <- svc.Status{State: svc.Stopped}
						return false, 0
					}
					slog.Info("runner: beginning stop sequence", "pid", h.process.PID(), "timeout", h.stopTimeout)
					h.beginStop()
					result := <-h.waitForExit(waitCh)
					slog.Info("runner: service stopped", "exit_code", result.code, "error", result.err)
					h.report(StateInactive, 0, result.code, "")
					changes <- svc.Status{State: svc.Stopped}
					return false, 0
				}
			}
		}
	}
}

func (h *SCMHandler) report(state string, pid uint32, exitCode int, lastError string) {
	if h.reporter == nil {
		return
	}
	_ = h.reporter.Report(StateEvent{
		State:        state,
		PID:          pid,
		ExitCode:     exitCode,
		RestartCount: h.restart.RestartCount(),
		LastError:    lastError,
	})
}

func (h *SCMHandler) beginStop() {
	if h.service.ExecStop != "" {
		cmd, err := parseCommand(h.service.ExecStop)
		if err != nil {
			slog.Error("runner: parse ExecStop failed", "error", err)
			return
		}
		if cmd == nil {
			slog.Warn("runner: ExecStop is empty")
			return
		}
		env, err := buildEnvironment(h.service)
		if err != nil {
			slog.Error("runner: build ExecStop environment failed", "error", err)
			return
		}
		stdout, stderr := h.process.logWriters()
		slog.Info("runner: running ExecStop", "command", cmd.path, "args", cmd.args)
		go func() {
			if err := runCommand(*cmd, env, h.service.WorkingDirectory, stdout, stderr); err != nil {
				slog.Warn("runner: ExecStop failed", "error", err)
				return
			}
			slog.Info("runner: ExecStop finished")
		}()
		return
	}

	slog.Info("runner: no ExecStop configured, sending console interrupt", "pid", h.process.PID())
	if err := h.process.Interrupt(); err != nil {
		slog.Warn("runner: console interrupt failed", "pid", h.process.PID(), "error", err)
	}
}

func (h *SCMHandler) waitForExit(waitCh <-chan waitResult) <-chan waitResult {
	done := make(chan waitResult, 1)
	go func() {
		slog.Info("runner: waiting for process to exit", "pid", h.process.PID(), "timeout", h.stopTimeout)
		timer := time.NewTimer(h.stopTimeout)
		defer timer.Stop()
		select {
		case result := <-waitCh:
			slog.Info("runner: process exited during stop", "exit_code", result.code, "error", result.err)
			done <- result
		case <-timer.C:
			slog.Warn("runner: stop timeout reached, killing process", "pid", h.process.PID(), "timeout", h.stopTimeout)
			h.process.Kill()
			result := <-waitCh
			slog.Info("runner: killed process exited", "exit_code", result.code, "error", result.err)
			done <- result
		}
	}()
	return done
}
