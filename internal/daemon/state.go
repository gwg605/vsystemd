package daemon

import (
	"errors"
	"os"
	"time"

	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/unit"
)

type RunnerState string

const (
	RunnerStateActive       RunnerState = "active"
	RunnerStateInactive     RunnerState = "inactive"
	RunnerStateFailed       RunnerState = "failed"
	RunnerStateActivating   RunnerState = "activating"
	RunnerStateRestarting   RunnerState = "restarting"
	RunnerStateDeactivating RunnerState = "deactivating"
	RunnerStateDead         RunnerState = "dead"
)

type SystemdState string

const (
	SystemdActiveRunning         SystemdState = "active (running)"
	SystemdActivatingStart       SystemdState = "activating (start)"
	SystemdActivatingAutoRestart SystemdState = "activating (auto-restart)"
	SystemdInactiveDead          SystemdState = "inactive (dead)"
	SystemdActiveWaiting         SystemdState = "active (waiting)"
	SystemdFailed                SystemdState = "failed"
	SystemdDeactivatingStop      SystemdState = "deactivating (stop)"
)

type LoadState string

const (
	LoadStateLoaded     LoadState = "loaded"
	LoadStateBadSetting LoadState = "bad-setting"
	LoadStateError      LoadState = "error"
	LoadStateNotFound   LoadState = "not-found"
)

type ServiceState struct {
	Unit           string
	SCMState       scm.State
	RunnerState    RunnerState
	PID            uint32
	ExitCode       int
	RestartCount   int
	LastTransition time.Time
	LastSeen       time.Time
	LastError      string
	LoadState      LoadState
	LoadError      string
}

func (s ServiceState) Status() SystemdState {
	switch s.SCMState {
	case scm.StateStartPending:
		return SystemdActivatingStart
	case scm.StateStopPending:
		return SystemdDeactivatingStop
	case scm.StateRunning:
		switch s.RunnerState {
		case RunnerStateActivating:
			return SystemdActivatingStart
		case RunnerStateRestarting:
			return SystemdActivatingAutoRestart
		case RunnerStateDeactivating:
			return SystemdDeactivatingStop
		case RunnerStateDead:
			return SystemdInactiveDead
		default:
			return SystemdActiveRunning
		}
	case scm.StateStopped:
		if s.RunnerState == RunnerStateFailed && s.ExitCode != 0 {
			return SystemdFailed
		}
		return SystemdInactiveDead
	default:
		return SystemdInactiveDead
	}
}

type loadError struct {
	state LoadState
	err   error
}

func (e *loadError) Error() string { return e.err.Error() }

func (e *loadError) Unwrap() error { return e.err }

func (d *Daemon) loadUnit(id unit.ID) (*unit.UnitFile, error) {
	data, err := d.store.ReadUnit(id.String())
	if err != nil {
		return nil, &loadError{state: classifyLoadError(err), err: err}
	}
	parsed, err := unit.Parse(id, data)
	if err != nil {
		return nil, &loadError{state: LoadStateBadSetting, err: err}
	}
	return parsed, nil
}

func classifyLoadError(err error) LoadState {
	if errors.Is(err, os.ErrNotExist) {
		return LoadStateNotFound
	}
	return LoadStateError
}

func loadErrorState(err error) (LoadState, bool) {
	var le *loadError
	if errors.As(err, &le) {
		return le.state, true
	}
	return "", false
}

func (d *Daemon) markLoadError(id unit.ID, err error) {
	var le *loadError
	if errors.As(err, &le) {
		d.setLoadError(id, le.state, le.err)
		return
	}
	d.setLoadError(id, LoadStateBadSetting, err)
}

func (d *Daemon) setLoadError(id unit.ID, state LoadState, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	name := id.String()
	current := d.states[name]
	current.Unit = name
	current.LoadState = state
	current.LoadError = err.Error()
	d.states[name] = current
}

func (d *Daemon) clearLoadError(id unit.ID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	name := id.String()
	current, ok := d.states[name]
	if !ok || current.LoadState == "" || current.LoadState == LoadStateLoaded {
		return
	}
	current.LoadState = LoadStateLoaded
	current.LoadError = ""
	d.states[name] = current
}

func (d *Daemon) loadState(id unit.ID) (LoadState, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	state := d.states[id.String()]
	if state.LoadState == "" {
		return LoadStateLoaded, ""
	}
	return state.LoadState, state.LoadError
}

func (d *Daemon) forgetUnit(id unit.ID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.states, id.String())
}

func (d *Daemon) pruneStates(existing map[string]bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for name := range d.states {
		if !existing[name] {
			delete(d.states, name)
		}
	}
}
