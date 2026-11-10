package daemon

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

func (d *Daemon) handleStatusRequest(sourcePeer string, msg transport.Message) error {
	var request protocol.StatusRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}

	response, err := d.status(request.Unit)
	if err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	return d.respond(sourcePeer, protocol.TypeStatusResponse, msg.ID, response)
}

func (d *Daemon) handleListUnits(sourcePeer string, msg transport.Message) error {
	response, err := d.listUnits()
	if err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	return d.respond(sourcePeer, protocol.TypeListUnitsResponse, msg.ID, response)
}

func (d *Daemon) handleDaemonReload(sourcePeer string, msg transport.Message) error {
	if err := d.reload(); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	return d.respond(sourcePeer, protocol.TypeReloadResult, msg.ID, protocol.ReloadResult{Success: true})
}

func (d *Daemon) respond(targetPeer, msgType, requestID string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s: %w", msgType, err)
	}
	return d.transport.Send(targetPeer, transport.Message{
		Type:      msgType,
		RequestID: requestID,
		Payload:   data,
	})
}

func (d *Daemon) respondError(targetPeer, requestID string, err error) error {
	return d.respond(targetPeer, protocol.TypeErrorResponse, requestID, protocol.ErrorResponse{Error: err.Error()})
}

func (d *Daemon) status(name string) (protocol.StatusResponse, error) {
	cached, hasCached := d.State(name)
	serviceName := strings.TrimSuffix(name, filepath.Ext(name))
	scmStatus, scmErr := d.manager.QueryStatus(serviceName)

	if scmErr != nil && !hasCached {
		if _, err := d.store.ReadUnit(name); err != nil {
			return protocol.StatusResponse{}, fmt.Errorf("unit %s not found", name)
		}
	}

	state := cached
	state.Unit = name
	if scmErr == nil {
		state.SCMState = scmStatus.State
		if state.PID == 0 {
			state.PID = scmStatus.PID
		}
		if state.ExitCode == 0 && scmStatus.ExitCode != 0 {
			state.ExitCode = int(scmStatus.ExitCode)
		}
	} else {
		state.SCMState = 0
	}

	logTail, err := d.store.ReadLogTail(name, 10)
	if err != nil {
		return protocol.StatusResponse{}, fmt.Errorf("read log tail: %w", err)
	}

	return protocol.StatusResponse{
		Unit:           name,
		SystemdState:   string(state.Status()),
		SCMState:       state.SCMState,
		PID:            state.PID,
		ExitCode:       state.ExitCode,
		RestartCount:   state.RestartCount,
		LastTransition: state.LastTransition,
		LastError:      state.LastError,
		LogTail:        logTail,
	}, nil
}

func (d *Daemon) listUnits() (protocol.ListUnitsResponse, error) {
	unitNames, err := d.store.ListUnits()
	if err != nil {
		return protocol.ListUnitsResponse{}, fmt.Errorf("list units: %w", err)
	}
	services, err := d.manager.ListServices()
	if err != nil {
		return protocol.ListUnitsResponse{}, fmt.Errorf("list services: %w", err)
	}

	scmByName := make(map[string]scm.ServiceStatus, len(services))
	for _, service := range services {
		scmByName[service.Name] = service
	}

	seen := make(map[string]bool)
	units := make([]protocol.UnitStatus, 0, len(unitNames)+len(services))
	for _, name := range unitNames {
		seen[name] = true

		data, err := d.store.ReadUnit(name)
		if err != nil {
			return protocol.ListUnitsResponse{}, fmt.Errorf("read unit %s: %w", name, err)
		}
		parsed, err := unit.Parse(name, data)
		if err != nil {
			return protocol.ListUnitsResponse{}, fmt.Errorf("parse unit %s: %w", name, err)
		}

		entry := protocol.UnitStatus{Unit: name, Description: parsed.Description}
		if strings.HasSuffix(name, ".timer") {
			entry.Type = "timer"
			entry.SystemdState = string(SystemdInactiveDead)
			fillTimerState(&entry, d, name)
			units = append(units, entry)
			continue
		}

		entry.Type = "service"
		state := d.serviceState(name, scmByName)
		entry.SystemdState = string(state.Status())
		entry.SCMState = state.SCMState
		units = append(units, entry)
	}

	for _, service := range services {
		name := service.Name + ".service"
		if seen[name] {
			continue
		}
		state := d.serviceState(name, scmByName)
		units = append(units, protocol.UnitStatus{
			Unit:         name,
			Type:         "service",
			SystemdState: string(state.Status()),
			SCMState:     state.SCMState,
		})
	}

	sort.Slice(units, func(i, j int) bool {
		return units[i].Unit < units[j].Unit
	})
	return protocol.ListUnitsResponse{Units: units}, nil
}

func (d *Daemon) serviceState(name string, services map[string]scm.ServiceStatus) ServiceState {
	state, _ := d.State(name)
	state.Unit = name
	if service, ok := services[strings.TrimSuffix(name, ".service")]; ok {
		state.SCMState = service.State
	}
	return state
}

func fillTimerState(entry *protocol.UnitStatus, d *Daemon, name string) {
	timerState, err := d.store.ReadTimerState(name)
	if err != nil {
		return
	}

	enabled := timerState.Enabled
	running := d.sched.Has(name)
	entry.Enabled = &enabled
	entry.Running = &running
	if !timerState.LastRun.IsZero() {
		lastRun := timerState.LastRun
		entry.LastRun = &lastRun
	}
	if !timerState.NextRun.IsZero() {
		nextRun := timerState.NextRun
		entry.NextRun = &nextRun
	}
}
