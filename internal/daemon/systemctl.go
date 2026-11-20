package daemon

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"

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

func (d *Daemon) handleVerifyUnitFile(source transport.Peer, msg transport.Message) error {
	var request protocol.VerifyUnitFileRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return d.respondError(source, msg.ID, err)
	}
	id, err := unit.ParseID(request.Unit)
	if err != nil {
		return d.respondError(source, msg.ID, err)
	}
	if err := d.verifyUnitFile(id, request.Content); err != nil {
		return d.respondError(source, msg.ID, err)
	}
	return d.respond(source, protocol.TypeVerifyUnitResult, msg.ID, protocol.VerifyUnitResult{Success: true})
}

// verifyUnitFile checks that the given content matches the unit in the list.
func (d *Daemon) verifyUnitFile(id unit.ID, content []byte) error {
	entry, ok := d.units.entry(id)
	if !ok {
		return fmt.Errorf("unit %s is not in the daemon list; run daemon-reload", id)
	}
	if entry.loadState != LoadStateLoaded {
		return fmt.Errorf("unit %s: %s", id, entry.loadError)
	}
	if sha256.Sum256(content) != entry.contentHash {
		return fmt.Errorf("unit %s changed since daemon-reload", id)
	}
	return nil
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
	entry, inList := d.units.entry(id)

	if !inList && errors.Is(scmErr, scm.ErrNotFound) {
		d.forgetUnit(id)
		return protocol.StatusResponse{}, fmt.Errorf("unit %s not found", name)
	}

	loadState := LoadStateNotFound
	loadError := errConfigurationMissing.Error()
	description := ""
	if inList {
		loadState = entry.loadState
		loadError = entry.loadError
		if entry.parsed != nil {
			description = entry.parsed.Description
		}
	}

	state := cached
	state.Unit = name
	scmState := scm.State(0)
	if scmErr == nil {
		scmState = scmStatus.State
		if state.PID == 0 {
			state.PID = scmStatus.PID
		}
		if state.ExitCode == 0 && scmStatus.ExitCode != 0 {
			state.ExitCode = int(scmStatus.ExitCode)
		}
	}

	return protocol.StatusResponse{
		Unit:           name,
		SystemdState:   string(state.Status(scmState)),
		SCMState:       scmState,
		PID:            state.PID,
		ExitCode:       state.ExitCode,
		RestartCount:   state.RestartCount,
		LastTransition: state.LastTransition,
		LastError:      state.LastError,
		LogTail:        d.logTail(id),
		Description:    description,
		LoadState:      string(loadState),
		LoadError:      loadError,
		Enabled:        serviceEnabled(d.manager, id.Name),
	}, nil
}

func serviceEnabled(manager scm.Manager, name string) *bool {
	cfg, err := manager.GetService(name)
	if err != nil {
		return nil
	}
	enabled := cfg.StartType != scm.StartTypeDisabled && cfg.StartType != scm.StartTypeManual
	return &enabled
}

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

func (d *Daemon) timerStatus(id unit.ID) (protocol.StatusResponse, error) {
	name := id.String()
	entry, inList := d.units.entry(id)
	if !inList {
		d.forgetUnit(id)
		return protocol.StatusResponse{}, fmt.Errorf("unit %s not found", name)
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
		LoadState:    string(entry.loadState),
		LoadError:    entry.loadError,
	}
	if entry.parsed != nil {
		response.Description = entry.parsed.Description
		if target, err := timerTarget(entry.parsed.Timer, id); err == nil {
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
	entry, ok := d.units.entry(id)
	if !ok || entry.parsed == nil || entry.parsed.Service == nil {
		return nil
	}
	name := id.String()

	stdout, stderr, err := entry.parsed.Service.ResolveOutputs()
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
