package daemon

import (
	"fmt"
	"sort"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/unit"
)

func (d *Daemon) listUnits() (protocol.ListUnitsResponse, error) {
	statuses, err := d.manager.QueryStatuses()
	if err != nil {
		return protocol.ListUnitsResponse{}, fmt.Errorf("query service statuses: %w", err)
	}
	scmByName := make(map[string]scm.ServiceStatus, len(statuses))
	for _, status := range statuses {
		scmByName[status.Name] = status
	}

	entries := d.units.all()
	units := make([]protocol.UnitStatus, 0, len(entries))
	for _, entry := range entries {
		id := entry.id
		loadState := entry.loadState
		if loadState == "" {
			loadState = LoadStateLoaded
		}
		item := protocol.UnitStatus{Unit: id.String(), LoadState: string(loadState)}

		if id.Type == unit.TypeTimer {
			item.Type = "timer"
			if entry.parsed != nil {
				item.Description = entry.parsed.Description
				if target, err := timerTarget(entry.parsed.Timer, id); err == nil {
					item.Activates = target.String()
				}
			}
			fillTimerState(&item, d, id)
			units = append(units, item)
			continue
		}

		item.Type = "service"
		if entry.parsed != nil {
			item.Description = entry.parsed.Description
		}
		scmState := scmByName[id.Name].State
		state := d.runnerState(id)
		item.SystemdState = string(state.Status(scmState))
		item.SCMState = scmState
		units = append(units, item)
	}

	sort.Slice(units, func(i, j int) bool {
		return units[i].Unit < units[j].Unit
	})
	return protocol.ListUnitsResponse{Units: units}, nil
}

func (d *Daemon) runnerState(id unit.ID) ServiceState {
	state, _ := d.State(id.String())
	state.Unit = id.String()
	return state
}

func fillTimerState(entry *protocol.UnitStatus, d *Daemon, id unit.ID) {
	timerState, err := d.store.ReadTimerState(id.String())
	if err != nil {
		return
	}

	enabled := timerState.Enabled
	running := d.sched.Has(id.String())
	entry.Enabled = &enabled
	entry.Running = &running
	if running {
		entry.SystemdState = string(SystemdActiveWaiting)
	} else {
		entry.SystemdState = string(SystemdInactiveDead)
	}
	if !timerState.LastRun.IsZero() {
		lastRun := timerState.LastRun
		entry.LastRun = &lastRun
	}
	if !timerState.NextRun.IsZero() {
		nextRun := timerState.NextRun
		entry.NextRun = &nextRun
	}
}
