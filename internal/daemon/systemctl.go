package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"

	"github.com/gwg605/vsystemd/internal/config"
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
	id, err := unit.ParseID(request.Unit)
	if err != nil {
		return d.respondError(source, msg.ID, err)
	}
	slog.Debug("daemon: status request", "from", source.String(), "unit", id)

	response, err := d.status(id)
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

func (d *Daemon) status(id unit.ID) (protocol.StatusResponse, error) {
	if id.Type == unit.TypeTimer {
		return d.timerStatus(id)
	}

	name := id.String()
	cached, _ := d.State(name)
	scmStatus, scmErr := d.manager.QueryStatus(id.Name)

	data, readErr := d.store.ReadUnit(name)
	if errors.Is(scmErr, scm.ErrNotFound) && readErr != nil {
		d.forgetUnit(id)
		return protocol.StatusResponse{}, fmt.Errorf("unit %s not found", name)
	}

	description := ""
	if readErr == nil {
		parsed, parseErr := unit.Parse(id, data)
		if parseErr != nil {
			d.setConfigError(id, parseErr)
			slog.Warn("daemon: invalid unit", "unit", id, "error", parseErr)
		} else {
			d.clearConfigError(id)
			description = parsed.Description
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

	logTail := d.logTail(id)

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
		Description:    description,
		ConfigError:    d.configError(id),
		Enabled:        serviceEnabled(d.manager, id.Name),
	}, nil
}

func serviceEnabled(manager scm.Manager, name string) *bool {
	configs, err := manager.ListServices()
	if err != nil {
		return nil
	}
	for _, cfg := range configs {
		if cfg.Name != name {
			continue
		}
		enabled := cfg.StartType != scm.StartTypeDisabled && cfg.StartType != scm.StartTypeManual
		return &enabled
	}
	return nil
}

func (d *Daemon) listUnits() (protocol.ListUnitsResponse, error) {
	unitNames, err := d.store.ListUnits()
	if err != nil {
		return protocol.ListUnitsResponse{}, fmt.Errorf("list units: %w", err)
	}
	configs, err := d.manager.ListServices()
	if err != nil {
		return protocol.ListUnitsResponse{}, fmt.Errorf("list services: %w", err)
	}
	statuses, err := d.manager.QueryStatuses()
	if err != nil {
		return protocol.ListUnitsResponse{}, fmt.Errorf("query service statuses: %w", err)
	}

	scmByName := make(map[string]scm.ServiceStatus, len(statuses))
	for _, status := range statuses {
		scmByName[status.Name] = status
	}

	seen := make(map[string]bool)
	existing := make(map[string]bool)
	units := make([]protocol.UnitStatus, 0, len(unitNames)+len(configs))
	for _, name := range unitNames {
		id, err := unit.ParseID(name)
		if err != nil {
			slog.Warn("daemon: skip unit", "unit", name, "error", err)
			continue
		}
		existing[id.String()] = true
		if id.Name == config.ManagerServiceName {
			continue
		}
		seen[id.String()] = true

		parsed, parseErr := d.loadUnit(id)
		if parseErr != nil {
			d.setConfigError(id, parseErr)
			slog.Warn("daemon: invalid unit", "unit", id, "error", parseErr)
		} else {
			d.clearConfigError(id)
		}

		entry := protocol.UnitStatus{Unit: id.String(), ConfigError: d.configError(id)}
		if id.Type == unit.TypeTimer {
			entry.Type = "timer"
			if parseErr == nil {
				if target, err := timerTarget(parsed.Timer, id); err == nil {
					entry.Activates = target.String()
				}
			}
			fillTimerState(&entry, d, id)
			units = append(units, entry)
			continue
		}

		entry.Type = "service"
		if parseErr == nil {
			entry.Description = parsed.Description
		}
		state := d.serviceState(id, scmByName)
		entry.SystemdState = string(state.Status())
		entry.SCMState = state.SCMState
		units = append(units, entry)
	}

	for _, cfg := range configs {
		id := unit.ID{Name: cfg.Name, Type: unit.TypeService}
		existing[id.String()] = true
		if cfg.Name == config.ManagerServiceName {
			continue
		}
		if seen[id.String()] {
			continue
		}
		state := d.serviceState(id, scmByName)
		units = append(units, protocol.UnitStatus{
			Unit:         id.String(),
			Type:         "service",
			SystemdState: string(state.Status()),
			SCMState:     state.SCMState,
			Description:  cfg.Description,
			ConfigError:  d.configError(id),
		})
	}

	d.pruneStates(existing)

	sort.Slice(units, func(i, j int) bool {
		return units[i].Unit < units[j].Unit
	})
	return protocol.ListUnitsResponse{Units: units}, nil
}

func (d *Daemon) serviceState(id unit.ID, services map[string]scm.ServiceStatus) ServiceState {
	state, _ := d.State(id.String())
	state.Unit = id.String()
	if service, ok := services[id.Name]; ok {
		state.SCMState = service.State
	}
	return state
}

func (d *Daemon) timerStatus(id unit.ID) (protocol.StatusResponse, error) {
	name := id.String()
	data, err := d.store.ReadUnit(name)
	if err != nil {
		d.forgetUnit(id)
		return protocol.StatusResponse{}, fmt.Errorf("unit %s not found", name)
	}
	parsed, parseErr := unit.Parse(id, data)
	if parseErr != nil {
		d.setConfigError(id, parseErr)
		slog.Warn("daemon: invalid unit", "unit", id, "error", parseErr)
	} else {
		d.clearConfigError(id)
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
		ConfigError:  d.configError(id),
	}
	if parseErr == nil {
		response.Description = parsed.Description
		if target, err := timerTarget(parsed.Timer, id); err == nil {
			response.Activates = target.String()
			response.LogTail = d.logTail(target)
		}
	}
	if running && !state.ActiveSince.IsZero() {
		activeSince := state.ActiveSince
		response.ActiveSince = &activeSince
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

func (d *Daemon) logTail(id unit.ID) []string {
	name := id.String()
	data, err := d.store.ReadUnit(name)
	if err != nil {
		return nil
	}
	parsed, err := unit.Parse(id, data)
	if err != nil || parsed.Service == nil {
		return nil
	}

	stdout, stderr, err := parsed.Service.ResolveOutputs()
	if err != nil {
		slog.Warn("daemon: invalid log output config", "unit", name, "error", err)
		return nil
	}

	target := unit.StatusTarget(stdout, stderr)
	switch target.Kind {
	case unit.OutputNull:
		return nil
	case unit.OutputFile, unit.OutputAppend:
		if err := d.store.ValidatePath(target.Path); err != nil {
			slog.Warn("daemon: invalid log path", "unit", name, "path", target.Path, "error", err)
			return nil
		}
		lines, err := logwriter.ReadTail(filepath.Clean(target.Path), 10)
		if err != nil {
			slog.Warn("daemon: read log tail failed", "unit", name, "path", target.Path, "error", err)
			return nil
		}
		return lines
	default:
		path, err := logwriter.LatestFile(logwriter.ServiceLogDir(d.logsDir, id.Name))
		if err != nil {
			slog.Warn("daemon: find latest log failed", "unit", name, "error", err)
			return nil
		}
		if path == "" {
			return nil
		}
		lines, err := logwriter.ReadTail(path, 10)
		if err != nil {
			slog.Warn("daemon: read log tail failed", "unit", name, "path", path, "error", err)
			return nil
		}
		return lines
	}
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
