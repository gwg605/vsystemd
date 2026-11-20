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
	RunnerState    RunnerState
	PID            uint32
	ExitCode       int
	RestartCount   int
	LastTransition time.Time
	LastSeen       time.Time
	LastError      string
}

func (s ServiceState) Status(scmState scm.State) SystemdState {
	switch scmState {
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
