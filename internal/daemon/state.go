package daemon

import (
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
	ConfigError    string
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

func (d *Daemon) loadUnit(id unit.ID) (*unit.UnitFile, error) {
	data, err := d.store.ReadUnit(id.String())
	if err != nil {
		return nil, err
	}
	return unit.Parse(id, data)
}

func (d *Daemon) setConfigError(id unit.ID, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	name := id.String()
	state := d.states[name]
	state.Unit = name
	state.ConfigError = err.Error()
	d.states[name] = state
}

func (d *Daemon) clearConfigError(id unit.ID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	name := id.String()
	state, ok := d.states[name]
	if !ok || state.ConfigError == "" {
		return
	}
	state.ConfigError = ""
	d.states[name] = state
}

func (d *Daemon) configError(id unit.ID) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.states[id.String()].ConfigError
}
