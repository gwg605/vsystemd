//go:build windows

package runner

import (
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/unit"
)

type runPhase int

const (
	phaseStartPre runPhase = iota
	phaseStartPost
	phaseRunning
	phaseStopping
	phaseAutoRestart
	phaseStopped
	phaseFailed
)

type stopTrigger int

const (
	triggerControl stopTrigger = iota
	triggerExit
	triggerStartFailure
)

type stopStage int

const (
	stageGraceful stopStage = iota
	stageKill
)

type handlerSession struct {
	h        *controlHandler
	requests <-chan svc.ChangeRequest
	changes  chan<- svc.Status
	events   chan opResult

	phase     runPhase
	trigger   stopTrigger
	stage     stopStage
	exitCode  int
	lastError string
	attempts  int
	status    svc.Status

	ops map[opRole]operation

	stopTimerC    <-chan time.Time
	restartTimerC <-chan time.Time
}

type controlHandler struct {
	service     *unit.ServiceUnit
	restart     *RestartController
	reporter    *StateReporter
	stopTimeout time.Duration
}

func newControlHandler(service *unit.ServiceUnit, restart *RestartController) *controlHandler {
	return &controlHandler{service: service, restart: restart}
}

func (h *controlHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	return h.newSession(requests, changes).run()
}

func (h *controlHandler) newSession(requests <-chan svc.ChangeRequest, changes chan<- svc.Status) *handlerSession {
	return &handlerSession{
		h:        h,
		requests: requests,
		changes:  changes,
		events:   make(chan opResult, 16),
		ops:      make(map[opRole]operation),
	}
}

func (s *handlerSession) run() (ssec bool, errno uint32) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.handlePanic(recovered)
			ssec, errno = false, 1
		}
	}()

	s.beginStart()
	for s.phase != phaseStopped && s.phase != phaseFailed {
		select {
		case result := <-s.events:
			s.onOperationCompleted(result)
		case request := <-s.requests:
			s.onControl(request)
		case <-s.stopTimerC:
			s.onStopTimeout()
		case <-s.restartTimerC:
			s.onRestartTimeout()
		}
	}
	s.changes <- svc.Status{State: svc.Stopped}
	return false, 0
}

func (s *handlerSession) beginStart() {}

func (s *handlerSession) onOperationCompleted(res opResult) {}

func (s *handlerSession) onControl(request svc.ChangeRequest) {}

func (s *handlerSession) onStopTimeout() {}

func (s *handlerSession) onRestartTimeout() {}

func (s *handlerSession) enterStopping(trigger stopTrigger, exitCode int) {}

func (s *handlerSession) resolveStopping() {}

func (s *handlerSession) finishStopped() {}

func (s *handlerSession) finishFailed(message string) {}

func (s *handlerSession) startOperation(role opRole, op operation) {}

func (s *handlerSession) completeOperation(role opRole) bool { return false }

func (s *handlerSession) report(state string, pid uint32, exitCode int, lastError string) {}

func (s *handlerSession) handlePanic(recovered any) {}
