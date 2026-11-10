package daemon

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

const (
	messageStatusResponse    = "status_response"
	messageListUnits         = "list_units"
	messageListUnitsResponse = "list_units_response"
	messageDaemonReload      = "daemon_reload"
	messageReloadResult      = "reload_result"
	messageErrorResponse     = "error_response"
)

type statusRequest struct {
	Unit string `json:"unit"`
}

type statusResponse struct {
	Unit           string       `json:"unit"`
	SystemdState   SystemdState `json:"systemd_state"`
	SCMState       scm.State    `json:"scm_state"`
	PID            uint32       `json:"pid"`
	ExitCode       int          `json:"exit_code"`
	RestartCount   int          `json:"restart_count"`
	LastTransition time.Time    `json:"last_transition"`
	LastError      string       `json:"last_error"`
	LogTail        []string     `json:"log_tail"`
}

type unitStatus struct {
	Unit         string       `json:"unit"`
	Type         string       `json:"type"`
	SystemdState SystemdState `json:"systemd_state"`
	SCMState     scm.State    `json:"scm_state"`
	Description  string       `json:"description"`
	Enabled      *bool        `json:"enabled"`
	Running      *bool        `json:"running"`
	NextRun      *time.Time   `json:"next_run"`
	LastRun      *time.Time   `json:"last_run"`
}

type listUnitsResponse struct {
	Units []unitStatus `json:"units"`
}

type reloadResult struct {
	Success bool `json:"success"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (d *Daemon) handleStatusRequest(sourcePeer string, msg transport.Message) error {
	var request statusRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}

	response, err := d.status(request.Unit)
	if err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	return d.respond(sourcePeer, messageStatusResponse, msg.ID, response)
}

func (d *Daemon) handleListUnits(sourcePeer string, msg transport.Message) error {
	response, err := d.listUnits()
	if err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	return d.respond(sourcePeer, messageListUnitsResponse, msg.ID, response)
}

func (d *Daemon) handleDaemonReload(sourcePeer string, msg transport.Message) error {
	if err := d.reload(); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	return d.respond(sourcePeer, messageReloadResult, msg.ID, reloadResult{Success: true})
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
	return d.respond(targetPeer, messageErrorResponse, requestID, errorResponse{Error: err.Error()})
}

func (d *Daemon) status(name string) (statusResponse, error) {
	cached, hasCached := d.State(name)
	serviceName := strings.TrimSuffix(name, filepath.Ext(name))
	scmStatus, scmErr := d.manager.QueryStatus(serviceName)

	if scmErr != nil && !hasCached {
		if _, err := d.store.ReadUnit(name); err != nil {
			return statusResponse{}, fmt.Errorf("unit %s not found", name)
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
		return statusResponse{}, fmt.Errorf("read log tail: %w", err)
	}

	return statusResponse{
		Unit:           name,
		SystemdState:   state.Status(),
		SCMState:       state.SCMState,
		PID:            state.PID,
		ExitCode:       state.ExitCode,
		RestartCount:   state.RestartCount,
		LastTransition: state.LastTransition,
		LastError:      state.LastError,
		LogTail:        logTail,
	}, nil
}

func (d *Daemon) listUnits() (listUnitsResponse, error) {
	unitNames, err := d.store.ListUnits()
	if err != nil {
		return listUnitsResponse{}, fmt.Errorf("list units: %w", err)
	}
	services, err := d.manager.ListServices()
	if err != nil {
		return listUnitsResponse{}, fmt.Errorf("list services: %w", err)
	}

	scmByName := make(map[string]scm.ServiceStatus, len(services))
	for _, service := range services {
		scmByName[service.Name] = service
	}

	seen := make(map[string]bool)
	units := make([]unitStatus, 0, len(unitNames)+len(services))
	for _, name := range unitNames {
		seen[name] = true

		data, err := d.store.ReadUnit(name)
		if err != nil {
			return listUnitsResponse{}, fmt.Errorf("read unit %s: %w", name, err)
		}
		parsed, err := unit.Parse(name, data)
		if err != nil {
			return listUnitsResponse{}, fmt.Errorf("parse unit %s: %w", name, err)
		}

		entry := unitStatus{Unit: name, Description: parsed.Description}
		if strings.HasSuffix(name, ".timer") {
			entry.Type = "timer"
			entry.SystemdState = SystemdInactiveDead
			entry.fillTimerState(d, name)
			units = append(units, entry)
			continue
		}

		entry.Type = "service"
		state := d.serviceState(name, scmByName)
		entry.SystemdState = state.Status()
		entry.SCMState = state.SCMState
		units = append(units, entry)
	}

	for _, service := range services {
		name := service.Name + ".service"
		if seen[name] {
			continue
		}
		state := d.serviceState(name, scmByName)
		units = append(units, unitStatus{
			Unit:         name,
			Type:         "service",
			SystemdState: state.Status(),
			SCMState:     state.SCMState,
		})
	}

	sort.Slice(units, func(i, j int) bool {
		return units[i].Unit < units[j].Unit
	})
	return listUnitsResponse{Units: units}, nil
}

func (d *Daemon) serviceState(name string, services map[string]scm.ServiceStatus) ServiceState {
	state, _ := d.State(name)
	state.Unit = name
	if service, ok := services[strings.TrimSuffix(name, ".service")]; ok {
		state.SCMState = service.State
	}
	return state
}

func (entry *unitStatus) fillTimerState(d *Daemon, name string) {
	timerState, err := d.store.ReadTimerState(name)
	if err != nil {
		return
	}

	enabled := timerState.Enabled
	running := d.timerRunning(name)
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
