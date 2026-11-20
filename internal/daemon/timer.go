package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/timermgr"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

func (d *Daemon) handleTimerEnable(source transport.Peer, msg transport.Message) error {
	var request protocol.TimerRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return d.respondError(source, msg.ID, err)
	}
	id, err := unit.ParseID(request.Unit)
	if err != nil {
		return d.respondError(source, msg.ID, err)
	}
	if err := d.validateTimer(id); err != nil {
		return d.respondError(source, msg.ID, err)
	}
	slog.Info("daemon: timer enable", "unit", id, "enabled", request.Enabled)

	if err := d.timers.Enable(id.String(), request.Enabled); err != nil {
		return d.respondError(source, msg.ID, err)
	}

	response, err := d.timerResult(id.String())
	if err != nil {
		return d.respondError(source, msg.ID, err)
	}
	return d.respond(source, protocol.TypeTimerResult, msg.ID, response)
}

func (d *Daemon) handleTimerStart(source transport.Peer, msg transport.Message) error {
	var request protocol.TimerRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return d.respondError(source, msg.ID, err)
	}
	id, err := unit.ParseID(request.Unit)
	if err != nil {
		return d.respondError(source, msg.ID, err)
	}
	if err := d.validateTimer(id); err != nil {
		return d.respondError(source, msg.ID, err)
	}
	slog.Info("daemon: timer start", "unit", id)

	if err := d.timers.StartTimer(id.String()); err != nil {
		return d.respondError(source, msg.ID, err)
	}

	response, err := d.timerResult(id.String())
	if err != nil {
		return d.respondError(source, msg.ID, err)
	}
	return d.respond(source, protocol.TypeTimerResult, msg.ID, response)
}

func (d *Daemon) handleTimerStop(source transport.Peer, msg transport.Message) error {
	var request protocol.TimerRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return d.respondError(source, msg.ID, err)
	}
	id, err := unit.ParseID(request.Unit)
	if err != nil {
		return d.respondError(source, msg.ID, err)
	}
	if err := d.validateTimer(id); err != nil {
		return d.respondError(source, msg.ID, err)
	}
	slog.Info("daemon: timer stop", "unit", id)

	if err := d.timers.StopTimer(id.String()); err != nil {
		return d.respondError(source, msg.ID, err)
	}

	response, err := d.timerResult(id.String())
	if err != nil {
		return d.respondError(source, msg.ID, err)
	}
	return d.respond(source, protocol.TypeTimerResult, msg.ID, response)
}

func (d *Daemon) timerResult(name string) (protocol.TimerResult, error) {
	status, err := d.timers.QueryStatus(name)
	if err != nil {
		return protocol.TimerResult{}, fmt.Errorf("read timer %s: %w", name, err)
	}
	return protocol.TimerResult{
		Unit:    name,
		Success: true,
		Enabled: status.Enabled,
		Running: status.Running,
		LastRun: status.LastRun,
		NextRun: status.NextRun,
	}, nil
}

func (d *Daemon) validateTimer(id unit.ID) error {
	if id.Type != unit.TypeTimer {
		return fmt.Errorf("unit %s is not a timer", id)
	}
	entry, ok := d.units.entry(id)
	if !ok {
		return fmt.Errorf("timer %s not found", id)
	}
	if entry.loadState != LoadStateLoaded {
		return fmt.Errorf("timer %s: %s", id, entry.loadError)
	}
	return nil
}

// onTimerEvent starts the service a timer activates. The manager reports only
// the timer name; the target service is resolved from the timer unit.
func (d *Daemon) onTimerEvent(name string, status timermgr.TimerStatus) {
	id, err := unit.ParseID(name)
	if err != nil {
		slog.Warn("daemon: timer fired for invalid unit", "unit", name, "error", err)
		return
	}
	entry, ok := d.units.entry(id)
	if !ok || entry.parsed == nil || entry.parsed.Timer == nil {
		slog.Warn("daemon: timer fired for unknown unit", "unit", name)
		return
	}
	target, err := timerTarget(entry.parsed.Timer, id)
	if err != nil {
		slog.Error("daemon: resolve timer target failed", "unit", name, "error", err)
		return
	}
	slog.Info("daemon: timer fired", "unit", name, "service", target)
	if err := d.manager.StartService(target.Name); err != nil {
		slog.Error("daemon: start service failed", "unit", name, "service", target, "error", err)
	}
}

// timerTarget returns the service a timer activates, defaulting to a service
// with the same name as the timer.
func timerTarget(timer *unit.TimerUnit, id unit.ID) (unit.ID, error) {
	if timer.Unit != "" {
		return unit.ParseID(timer.Unit)
	}
	return unit.ID{Name: id.Name, Type: unit.TypeService}, nil
}

// syncTimers reconciles the timer database with the unit files: it upserts the
// configuration of every loaded timer and deletes timers whose unit file is
// gone, unless they are still enabled or running.
func (d *Daemon) syncTimers() error {
	slog.Info("daemon: syncing timers")
	configs, err := d.timers.ListTimers()
	if err != nil {
		return fmt.Errorf("list timers: %w", err)
	}

	present := make(map[string]bool)
	for _, entry := range d.units.all() {
		if entry.id.Type != unit.TypeTimer || !entry.hasFile {
			continue
		}
		name := entry.id.String()
		present[name] = true
		if entry.loadState != LoadStateLoaded || entry.parsed == nil {
			continue
		}
		cfg := timermgr.TimerConfig{Name: name, OnCalendar: entry.parsed.Timer.OnCalendar}
		if err := d.timers.SetTimer(cfg); err != nil {
			slog.Warn("daemon: set timer failed", "unit", name, "error", err)
		}
	}

	for _, cfg := range configs {
		if present[cfg.Name] {
			continue
		}
		if err := d.timers.DeleteTimer(cfg.Name); err != nil {
			slog.Warn("daemon: keeping timer without unit file", "unit", cfg.Name, "error", err)
		}
	}
	return nil
}

func (d *Daemon) timerStatus(id unit.ID) (protocol.StatusResponse, error) {
	name := id.String()
	entry, inList := d.units.entry(id)
	if !inList {
		d.forgetUnit(id)
		return protocol.StatusResponse{}, fmt.Errorf("unit %s not found", name)
	}

	status, err := d.timers.QueryStatus(name)
	if err != nil {
		status = timermgr.TimerStatus{Name: name}
	}

	systemdState := SystemdInactiveDead
	if status.Running {
		systemdState = SystemdActiveWaiting
	}

	enabled := status.Enabled
	running := status.Running
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
	if status.Running && !status.ActiveSince.IsZero() {
		activeSince := status.ActiveSince
		response.ActiveSince = &activeSince
	}
	if !status.LastRun.IsZero() {
		lastRun := status.LastRun
		response.LastRun = &lastRun
	}
	if !status.NextRun.IsZero() {
		nextRun := status.NextRun
		response.NextRun = &nextRun
	}
	return response, nil
}
