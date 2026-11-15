package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

func (d *Daemon) handleStatusRequest(source transport.Peer, msg transport.Message) error {
	var request protocol.StatusRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return d.respondError(source, msg.ID, err)
	}
	slog.Debug("daemon: status request", "from", source.String(), "unit", request.Unit)

	response, err := d.status(request.Unit)
	if err != nil {
		return d.respondError(source, msg.ID, err)
	}
	return d.respond(source, protocol.TypeStatusResponse, msg.ID, response)
}

func (d *Daemon) handleListUnits(source transport.Peer, msg transport.Message) error {
	slog.Debug("daemon: list units request", "from", source.String())
	response, err := d.listUnits()
	if err != nil {
		return d.respondError(source, msg.ID, err)
	}
	return d.respond(source, protocol.TypeListUnitsResponse, msg.ID, response)
}

func (d *Daemon) handleDaemonReload(source transport.Peer, msg transport.Message) error {
	slog.Info("daemon: reload request", "from", source.String())
	if err := d.reload(); err != nil {
		return d.respondError(source, msg.ID, err)
	}
	return d.respond(source, protocol.TypeReloadResult, msg.ID, protocol.ReloadResult{Success: true})
}

func (d *Daemon) respond(target transport.Peer, msgType, requestID string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("daemon: encode response failed", "type", msgType, "error", err)
		return fmt.Errorf("encode %s: %w", msgType, err)
	}
	if err := d.transport.Send(target, transport.Message{
		Type:      msgType,
		RequestID: requestID,
		Payload:   data,
	}); err != nil {
		slog.Warn("daemon: send response failed", "to", target.String(), "type", msgType, "error", err)
		return err
	}
	slog.Debug("daemon: response sent", "to", target.String(), "type", msgType, "request_id", requestID)
	return nil
}

func (d *Daemon) respondError(target transport.Peer, requestID string, err error) error {
	slog.Warn("daemon: request failed", "to", target.String(), "request_id", requestID, "error", err)
	return d.respond(target, protocol.TypeErrorResponse, requestID, protocol.ErrorResponse{Error: err.Error()})
}

func (d *Daemon) status(name string) (protocol.StatusResponse, error) {
	if strings.HasSuffix(name, ".timer") {
		return d.timerStatus(name)
	}

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

	logTail := d.logTail(name)

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
			entry.Activates = timerTarget(parsed.Timer, name)
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

func (d *Daemon) timerStatus(name string) (protocol.StatusResponse, error) {
	data, err := d.store.ReadUnit(name)
	if err != nil {
		return protocol.StatusResponse{}, fmt.Errorf("unit %s not found", name)
	}
	parsed, err := unit.Parse(name, data)
	if err != nil {
		return protocol.StatusResponse{}, fmt.Errorf("parse unit %s: %w", name, err)
	}

	state, err := d.store.ReadTimerState(name)
	if err != nil {
		return protocol.StatusResponse{}, fmt.Errorf("read timer state %s: %w", name, err)
	}

	running := d.sched.Has(name)
	enabled := state.Enabled
	systemdState := SystemdInactiveDead
	if running {
		systemdState = SystemdActiveWaiting
	}

	response := protocol.StatusResponse{
		Unit:         name,
		SystemdState: string(systemdState),
		Enabled:      &enabled,
		Running:      &running,
		Activates:    timerTarget(parsed.Timer, name),
	}
	if !state.LastRun.IsZero() {
		lastRun := state.LastRun
		response.LastRun = &lastRun
	}
	if !state.NextRun.IsZero() {
		nextRun := state.NextRun
		response.NextRun = &nextRun
	}
	return response, nil
}

func (d *Daemon) logTail(unitName string) []string {
	data, err := d.store.ReadUnit(unitName)
	if err != nil {
		return nil
	}
	parsed, err := unit.Parse(unitName, data)
	if err != nil || parsed.Service == nil {
		return nil
	}

	stdout, stderr, warnings := logwriter.ResolveServiceOutputs(parsed.Service)
	for _, warning := range warnings {
		slog.Warn("daemon: log output warning", "unit", unitName, "warning", warning)
	}

	target := logwriter.StatusTarget(stdout, stderr)
	switch target.Kind {
	case logwriter.OutputNull:
		return nil
	case logwriter.OutputFile, logwriter.OutputAppend:
		lines, err := logwriter.ReadTail(target.Path, 10)
		if err != nil {
			slog.Warn("daemon: read log tail failed", "unit", unitName, "path", target.Path, "error", err)
			return nil
		}
		return lines
	default:
		serviceName := strings.TrimSuffix(unitName, filepath.Ext(unitName))
		path, err := logwriter.LatestFile(logwriter.ServiceLogDir(d.logsDir, serviceName))
		if err != nil {
			slog.Warn("daemon: find latest log failed", "unit", unitName, "error", err)
			return nil
		}
		if path == "" {
			return nil
		}
		lines, err := logwriter.ReadTail(path, 10)
		if err != nil {
			slog.Warn("daemon: read log tail failed", "unit", unitName, "path", path, "error", err)
			return nil
		}
		return lines
	}
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
