//go:build windows

package runner

import (
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"runtime/debug"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/storage"
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

const defaultStopTimeout = 45 * time.Second

func resolveStopTimeout(service *unit.ServiceUnit) time.Duration {
	if service.TimeoutStopSec > 0 {
		return service.TimeoutStopSec
	}
	return defaultStopTimeout
}

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

	stopTimer     *time.Timer
	restartTimer  *time.Timer
	stopTimerC    <-chan time.Time
	restartTimerC <-chan time.Time
}

type controlHandler struct {
	service     *unit.ServiceUnit
	restart     *restartController
	reporter    *stateReporter
	env         environmentDefaults
	logs        *logStreamer
	store       storage.Storage
	stopTimeout time.Duration
}

func newControlHandler(service *unit.ServiceUnit, restart *restartController, env environmentDefaults, logs *logStreamer, store storage.Storage) *controlHandler {
	return &controlHandler{
		service:     service,
		restart:     restart,
		env:         env,
		logs:        logs,
		store:       store,
		stopTimeout: resolveStopTimeout(service),
	}
}

func (h *controlHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	return h.newSession(requests, changes).run()
}

func (h *controlHandler) setReporter(reporter *stateReporter) {
	h.reporter = reporter
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
	s.maybeResolve()
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
		s.maybeResolve()
	}
	return false, 0
}

func (s *handlerSession) beginStart() {
	if !s.h.restart.AllowStart() {
		s.finishFailed("start limit exceeded")
		return
	}
	if s.attempts > 0 {
		s.h.restart.RecordRestart()
	}
	s.attempts++

	s.phase = phaseStartPre
	s.setStatus(svc.Status{State: svc.StartPending})
	s.report(StateActivating, 0, s.exitCode, s.lastError)
	s.startPre()
}

func (s *handlerSession) startPre() {
	op, err := s.h.newPreOperation(s.reportResult)
	if err == nil {
		err = s.startOperation(rolePre, op)
	}
	if err != nil {
		s.lastError = err.Error()
		s.enterStopping(triggerStartFailure, s.exitCode)
	}
}

func (s *handlerSession) startMain() {
	op, err := s.h.newMainOperation(s.reportResult)
	if err == nil {
		err = s.startOperation(roleMain, op)
	}
	if err != nil {
		s.lastError = err.Error()
		s.enterStopping(triggerStartFailure, s.exitCode)
		return
	}
	s.phase = phaseStartPost
	s.startPost()
}

func (s *handlerSession) startPost() {
	op, err := s.h.newPostOperation(s.reportResult)
	if err == nil {
		err = s.startOperation(rolePost, op)
	}
	if err != nil {
		s.lastError = err.Error()
		s.enterStopping(triggerStartFailure, s.exitCode)
	}
}

func (s *handlerSession) startStop() {
	op, err := s.h.newStopOperation(s.reportResult)
	if err == nil {
		err = s.startOperation(roleStop, op)
	}
	if err != nil {
		s.lastError = err.Error()
		slog.Warn("runner: start ExecStop failed", "error", err)
	}
}

func (s *handlerSession) startOperation(role opRole, op operation) error {
	if op == nil {
		return fmt.Errorf("no operation for role %d", role)
	}
	s.ops[role] = op
	return op.start()
}

func (s *handlerSession) onOperationCompleted(result opResult) {
	s.completeOperation(result.role)
	switch result.role {
	case roleMain:
		s.onMainCompleted(result)
	case rolePre:
		if s.phase == phaseStartPre {
			s.onPreCompleted(result)
		}
	case rolePost:
		if s.phase == phaseStartPost {
			s.onPostCompleted(result)
		}
	}
}

func (s *handlerSession) onPreCompleted(result opResult) {
	if !s.operationSucceeded(result) {
		s.lastError = s.operationError(result)
		s.enterStopping(triggerStartFailure, result.code)
		return
	}
	s.startMain()
}

func (s *handlerSession) onPostCompleted(result opResult) {
	if !s.operationSucceeded(result) {
		s.lastError = s.operationError(result)
		s.enterStopping(triggerStartFailure, result.code)
		return
	}
	s.phase = phaseRunning
	s.setStatus(svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown})
	s.report(StateActive, s.mainPID(), 0, "")
}

func (s *handlerSession) onMainCompleted(result opResult) {
	s.exitCode = result.code
	s.lastError = s.operationError(result)
	switch s.phase {
	case phaseRunning, phaseStartPost:
		s.enterStopping(triggerExit, result.code)
	}
}

func (s *handlerSession) onControl(request svc.ChangeRequest) {
	switch request.Cmd {
	case svc.Interrogate:
		s.changes <- s.status
	case svc.Stop, svc.Shutdown:
		if s.phase == phaseStopping {
			return
		}
		s.enterStopping(triggerControl, s.exitCode)
	}
}

func (s *handlerSession) onStopTimeout() {
	s.stopTimerC = nil
	if s.phase != phaseStopping || s.stage != stageGraceful {
		return
	}
	s.stage = stageKill
	for _, op := range s.ops {
		op.cancel(cancelKill)
	}
}

func (s *handlerSession) onRestartTimeout() {
	s.restartTimerC = nil
	if s.phase != phaseAutoRestart {
		return
	}
	s.beginStart()
}

func (s *handlerSession) enterStopping(trigger stopTrigger, exitCode int) {
	s.trigger = trigger
	s.exitCode = exitCode
	s.stage = stageGraceful
	s.phase = phaseStopping
	if trigger == triggerControl {
		s.setStatus(svc.Status{State: svc.StopPending})
	}
	s.report(StateDeactivating, 0, s.exitCode, s.lastError)
	s.armStopTimer()
	s.beginDrain()
}

func (s *handlerSession) beginDrain() {
	main, hasMain := s.ops[roleMain]
	for role, op := range s.ops {
		switch role {
		case roleMain, roleStop:
			continue
		default:
			op.cancel(cancelKill)
		}
	}
	if !hasMain || s.trigger == triggerExit {
		return
	}
	if s.h.service.ExecStop != "" {
		s.startStop()
		return
	}
	mode := cancelInterrupt
	if s.trigger == triggerStartFailure {
		mode = cancelKill
	}
	main.cancel(mode)
}

func (s *handlerSession) resolveStopping() {
	s.stopTimers()
	switch s.trigger {
	case triggerControl:
		s.finishStopped()
	case triggerStartFailure:
		s.finishFailed(s.lastError)
	case triggerExit:
		if s.h.restart.ShouldRestart(s.exitCode) {
			s.enterAutoRestart()
			return
		}
		if s.exitCode == 0 {
			s.finishStopped()
			return
		}
		s.finishFailed(s.lastError)
	}
}

func (s *handlerSession) enterAutoRestart() {
	s.phase = phaseAutoRestart
	s.setStatus(svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown})
	s.report(StateRestarting, 0, s.exitCode, s.lastError)

	delay := s.h.restart.Delay()
	if delay <= 0 {
		s.beginStart()
		return
	}
	s.restartTimer = time.NewTimer(delay)
	s.restartTimerC = s.restartTimer.C
}

func (s *handlerSession) finishStopped() {
	s.stopTimers()
	s.phase = phaseStopped
	s.setStatus(svc.Status{State: svc.Stopped})
	s.report(StateInactive, 0, s.exitCode, "")
}

func (s *handlerSession) finishFailed(message string) {
	s.stopTimers()
	s.lastError = message
	s.phase = phaseFailed
	s.setStatus(svc.Status{State: svc.Stopped})
	s.report(StateFailed, 0, s.exitCode, message)
}

func (s *handlerSession) maybeResolve() {
	if s.phase == phaseStopping && len(s.ops) == 0 {
		s.resolveStopping()
	}
}

func (s *handlerSession) completeOperation(role opRole) bool {
	delete(s.ops, role)
	return len(s.ops) == 0
}

func (s *handlerSession) reportResult(result opResult) {
	s.events <- result
}

func (s *handlerSession) mainPID() uint32 {
	op, ok := s.ops[roleMain]
	if !ok {
		return 0
	}
	provider, ok := op.(pidProvider)
	if !ok {
		return 0
	}
	return provider.pid()
}

func (s *handlerSession) operationSucceeded(result opResult) bool {
	return result.err == nil && result.code == 0
}

func (s *handlerSession) operationError(result opResult) string {
	if result.err != nil {
		return result.err.Error()
	}
	if result.code != 0 {
		return fmt.Sprintf("exit code %d", result.code)
	}
	return ""
}

func (s *handlerSession) setStatus(status svc.Status) {
	s.status = status
	s.changes <- status
}

func (s *handlerSession) armStopTimer() {
	if s.h.stopTimeout <= 0 {
		s.stopTimerC = nil
		return
	}
	s.stopTimer = time.NewTimer(s.h.stopTimeout)
	s.stopTimerC = s.stopTimer.C
}

func (s *handlerSession) stopTimers() {
	if s.stopTimer != nil {
		s.stopTimer.Stop()
		s.stopTimer = nil
	}
	s.stopTimerC = nil
	if s.restartTimer != nil {
		s.restartTimer.Stop()
		s.restartTimer = nil
	}
	s.restartTimerC = nil
}

func (s *handlerSession) report(state string, pid uint32, exitCode int, lastError string) {
	if s.h.reporter == nil {
		return
	}
	if err := s.h.reporter.Report(StateEvent{
		State:        state,
		PID:          pid,
		ExitCode:     exitCode,
		RestartCount: s.h.restart.RestartCount(),
		LastError:    lastError,
	}); err != nil {
		slog.Warn("runner: report state failed", "state", state, "error", err)
	}
}

func (s *handlerSession) handlePanic(recovered any) {
	slog.Error("runner: panic in service handler",
		"panic", recovered,
		"stack", string(debug.Stack()))
	for _, op := range s.ops {
		op.cancel(cancelKill)
	}
	if s.phase != phaseStopped && s.phase != phaseFailed {
		s.phase = phaseFailed
		s.changes <- svc.Status{State: svc.Stopped}
	}
}

func (h *controlHandler) newPreOperation(report func(opResult)) (operation, error) {
	return h.newSequence(rolePre, h.service.ExecStartPre, report)
}

func (h *controlHandler) newPostOperation(report func(opResult)) (operation, error) {
	return h.newSequence(rolePost, h.service.ExecStartPost, report)
}

func (h *controlHandler) newSequence(role opRole, lines []string, report func(opResult)) (operation, error) {
	commands, err := parseCommands(lines)
	if err != nil {
		return nil, err
	}
	env, err := h.environment()
	if err != nil {
		return nil, err
	}
	stdout, stderr := h.writers()
	return newSequenceOperation(role, commands, env, h.service.WorkingDirectory, stdout, stderr, report), nil
}

func (h *controlHandler) newMainOperation(report func(opResult)) (operation, error) {
	return h.newProcess(roleMain, h.service.ExecStart, report)
}

func (h *controlHandler) newStopOperation(report func(opResult)) (operation, error) {
	return h.newProcess(roleStop, h.service.ExecStop, report)
}

func (h *controlHandler) newProcess(role opRole, line string, report func(opResult)) (operation, error) {
	parsed, err := parseCommand(line)
	if err != nil {
		return nil, err
	}
	if parsed == nil {
		return nil, fmt.Errorf("empty command for role %d", role)
	}
	env, err := h.environment()
	if err != nil {
		return nil, err
	}
	stdout, stderr := h.writers()
	cmd := exec.Command(parsed.path, parsed.args...)
	cmd.Dir = h.service.WorkingDirectory
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	configureCommand(cmd)
	op := newProcessOperation(role, cmd, report)
	op.interruptib = role == roleMain
	return op, nil
}

func (h *controlHandler) environment() ([]string, error) {
	return buildEnvironment(h.store, h.service, h.env)
}

func (h *controlHandler) writers() (io.Writer, io.Writer) {
	if h.logs == nil {
		return nil, nil
	}
	return h.logs.Stdout(), h.logs.Stderr()
}
