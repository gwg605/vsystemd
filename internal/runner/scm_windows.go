//go:build windows

package runner

import (
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

func (h *SCMHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	status := svc.Status{State: svc.StartPending}
	changes <- status
	h.restart.Reset()

	lastExit := 0
	lastError := ""

outer:
	for {
		if !h.restart.AllowStart() {
			h.report(StateFailed, 0, lastExit, "start limit exceeded")
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}

		h.report(StateActivating, 0, lastExit, lastError)
		if err := h.process.Start(); err != nil {
			lastError = err.Error()
			h.report(StateFailed, 0, lastExit, lastError)
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}

		status = svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
		changes <- status
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

				if !h.restart.ShouldRestart(result.code) {
					if result.code != 0 || result.err != nil {
						h.report(StateFailed, 0, lastExit, lastError)
					} else {
						h.report(StateInactive, 0, lastExit, "")
					}
					changes <- svc.Status{State: svc.Stopped}
					return false, 0
				}

				h.restart.RecordRestart()
				h.report(StateRestarting, 0, lastExit, lastError)
				if delay := h.restart.Delay(); delay > 0 {
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
					changes <- status
				case svc.Stop, svc.Shutdown:
					status = svc.Status{State: svc.StopPending}
					changes <- status
					if waitCh == nil {
						h.report(StateInactive, 0, lastExit, "")
						changes <- svc.Status{State: svc.Stopped}
						return false, 0
					}
					h.beginStop()
					result := <-h.waitForExit(waitCh)
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
		if err != nil || cmd == nil {
			return
		}
		env, err := buildEnvironment(h.service)
		if err != nil {
			return
		}
		stdout, stderr := h.process.logWriters()
		go func() {
			_ = runCommand(*cmd, env, h.service.WorkingDirectory, stdout, stderr)
		}()
		return
	}
	_ = h.process.Interrupt()
}

func (h *SCMHandler) waitForExit(waitCh <-chan waitResult) <-chan waitResult {
	done := make(chan waitResult, 1)
	go func() {
		timer := time.NewTimer(h.stopTimeout)
		defer timer.Stop()
		select {
		case result := <-waitCh:
			done <- result
		case <-timer.C:
			h.process.Kill()
			done <- <-waitCh
		}
	}()
	return done
}
