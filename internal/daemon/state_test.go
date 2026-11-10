package daemon

import (
	"testing"

	"github.com/gwg605/vsystemd/internal/scm"
)

func TestStatusComputation(t *testing.T) {
	tests := []struct {
		name        string
		scmState    scm.State
		runnerState RunnerState
		exitCode    int
		want        SystemdState
	}{
		{"running active", scm.StateRunning, RunnerStateActive, 0, SystemdActiveRunning},
		{"running activating", scm.StateRunning, RunnerStateActivating, 0, SystemdActivatingStart},
		{"running restarting", scm.StateRunning, RunnerStateRestarting, 0, SystemdActivatingAutoRestart},
		{"running no runner data", scm.StateRunning, "", 0, SystemdActiveRunning},
		{"running failed", scm.StateRunning, RunnerStateFailed, 1, SystemdActiveRunning},
		{"stopped inactive", scm.StateStopped, RunnerStateInactive, 0, SystemdInactiveDead},
		{"stopped failed", scm.StateStopped, RunnerStateFailed, 1, SystemdFailed},
		{"stopped failed zero exit", scm.StateStopped, RunnerStateFailed, 0, SystemdInactiveDead},
		{"stopped no runner data", scm.StateStopped, "", 0, SystemdInactiveDead},
		{"start pending", scm.StateStartPending, RunnerStateFailed, 1, SystemdActivatingStart},
		{"stop pending", scm.StateStopPending, RunnerStateActive, 0, SystemdDeactivatingStop},
		{"not registered", 0, RunnerStateActive, 0, SystemdInactiveDead},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := ServiceState{
				SCMState:    tt.scmState,
				RunnerState: tt.runnerState,
				ExitCode:    tt.exitCode,
			}
			if got := state.Status(); got != tt.want {
				t.Errorf("Status() = %q, want %q", got, tt.want)
			}
		})
	}
}
