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

outer:
	for {
		if !h.restart.AllowStart() {
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}
		if err := h.process.Start(); err != nil {
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}

		status = svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
		changes <- status

		waitCh := make(chan waitResult, 1)
		go func() {
			code, err := h.process.Wait()
			waitCh <- waitResult{code: code, err: err}
		}()

		var delayCh <-chan time.Time
		for {
			select {
			case result := <-waitCh:
				if !h.restart.ShouldRestart(result.code) {
					changes <- svc.Status{State: svc.Stopped}
					return false, 0
				}
				h.restart.RecordRestart()
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
						changes <- svc.Status{State: svc.Stopped}
						return false, 0
					}
					h.beginStop()
					<-h.waitForExit(waitCh)
					changes <- svc.Status{State: svc.Stopped}
					return false, 0
				}
			}
		}
	}
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
