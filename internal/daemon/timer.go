package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/scheduler"
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

	state, err := d.store.ReadTimerState(id.String())
	if err != nil {
		return d.respondError(source, msg.ID, err)
	}
	state.Enabled = request.Enabled
	if err := d.store.WriteTimerState(id.String(), state); err != nil {
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

	definition, err := d.timerDefinition(id)
	if err != nil {
		return d.respondError(source, msg.ID, err)
	}
	slog.Info("daemon: timer start", "unit", id, "service", definition.Service)
	if !d.sched.Has(definition.Name) {
		if err := d.sched.Add(definition); err != nil {
			return d.respondError(source, msg.ID, err)
		}
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

	if err := d.sched.Stop(id.String()); err != nil {
		return d.respondError(source, msg.ID, err)
	}

	response, err := d.timerResult(id.String())
	if err != nil {
		return d.respondError(source, msg.ID, err)
	}
	return d.respond(source, protocol.TypeTimerResult, msg.ID, response)
}

func (d *Daemon) timerResult(name string) (protocol.TimerResult, error) {
	state, err := d.store.ReadTimerState(name)
	if err != nil {
		return protocol.TimerResult{}, fmt.Errorf("read timer state %s: %w", name, err)
	}
	return protocol.TimerResult{
		Unit:    name,
		Success: true,
		Enabled: state.Enabled,
		Running: d.sched.Has(name),
		LastRun: state.LastRun,
		NextRun: state.NextRun,
	}, nil
}

func (d *Daemon) validateTimer(id unit.ID) error {
	if id.Type != unit.TypeTimer {
		return fmt.Errorf("unit %s is not a timer", id)
	}
	data, err := d.store.ReadUnit(id.String())
	if err != nil {
		return fmt.Errorf("timer %s not found", id)
	}
	if _, err := unit.Parse(id, data); err != nil {
		return fmt.Errorf("parse timer %s: %w", id, err)
	}
	return nil
}

func (d *Daemon) timerDefinition(id unit.ID) (scheduler.Timer, error) {
	if id.Type != unit.TypeTimer {
		return scheduler.Timer{}, fmt.Errorf("unit %s is not a timer", id)
	}
	data, err := d.store.ReadUnit(id.String())
	if err != nil {
		return scheduler.Timer{}, fmt.Errorf("timer %s not found", id)
	}
	parsed, err := unit.Parse(id, data)
	if err != nil {
		return scheduler.Timer{}, fmt.Errorf("parse timer %s: %w", id, err)
	}
	for _, warning := range parsed.Warnings {
		slog.Warn("daemon: ignored timer directive", "unit", id, "warning", warning)
	}
	if len(parsed.Timer.OnCalendar) == 0 {
		return scheduler.Timer{}, fmt.Errorf("timer %s has no OnCalendar", id)
	}

	target, err := timerTarget(parsed.Timer, id)
	if err != nil {
		return scheduler.Timer{}, err
	}
	if target.Name == config.ManagerServiceName {
		return scheduler.Timer{}, fmt.Errorf("timer %s targets the vsystemd manager service", id)
	}
	return scheduler.Timer{
		Name:       id.String(),
		Service:    target.Name,
		OnCalendar: parsed.Timer.OnCalendar,
	}, nil
}

// timerTarget returns the service a timer activates, defaulting to a service
// with the same name as the timer.
func timerTarget(timer *unit.TimerUnit, id unit.ID) (unit.ID, error) {
	if timer.Unit != "" {
		return unit.ParseID(timer.Unit)
	}
	return unit.ID{Name: id.Name, Type: unit.TypeService}, nil
}

func (d *Daemon) startTimers() error {
	slog.Info("daemon: starting enabled timers")
	units, err := d.store.ListUnits()
	if err != nil {
		return fmt.Errorf("list units: %w", err)
	}
	states, err := d.store.ListTimerStates()
	if err != nil {
		return fmt.Errorf("list timer states: %w", err)
	}

	known := make(map[string]bool)
	for _, name := range units {
		if !strings.HasSuffix(name, ".timer") {
			continue
		}
		id, err := unit.ParseID(name)
		if err != nil {
			slog.Warn("daemon: skip timer", "unit", name, "error", err)
			continue
		}
		known[id.String()] = true
		if !states[id.String()].Enabled {
			continue
		}
		definition, err := d.timerDefinition(id)
		if err != nil {
			slog.Warn("daemon: skip timer", "unit", id, "error", err)
			continue
		}
		if err := d.sched.Add(definition); err != nil {
			slog.Warn("daemon: add timer to scheduler failed", "unit", id, "error", err)
			continue
		}
		slog.Info("daemon: timer started", "unit", id, "service", definition.Service)
	}

	d.timerMu.Lock()
	d.knownTimers = known
	d.timerMu.Unlock()
	return nil
}

func (d *Daemon) reloadTimers() error {
	slog.Info("daemon: reloading timers")
	units, err := d.store.ListUnits()
	if err != nil {
		return fmt.Errorf("list units: %w", err)
	}
	states, err := d.store.ListTimerStates()
	if err != nil {
		return fmt.Errorf("list timer states: %w", err)
	}

	names := make(map[string]bool)
	var definitions []scheduler.Timer
	for _, name := range units {
		if !strings.HasSuffix(name, ".timer") {
			continue
		}
		id, err := unit.ParseID(name)
		if err != nil {
			slog.Warn("daemon: skip timer", "unit", name, "error", err)
			continue
		}
		names[id.String()] = true
		definition, err := d.timerDefinition(id)
		if err != nil {
			slog.Warn("daemon: skip timer", "unit", id, "error", err)
			continue
		}
		definitions = append(definitions, definition)
	}

	running := make(map[string]bool)
	for _, name := range d.sched.Names() {
		running[name] = true
	}

	d.timerMu.Lock()
	known := d.knownTimers
	d.knownTimers = names
	d.timerMu.Unlock()

	desired := make([]scheduler.Timer, 0, len(definitions))
	for _, definition := range definitions {
		if running[definition.Name] || (!known[definition.Name] && states[definition.Name].Enabled) {
			desired = append(desired, definition)
		}
	}
	if err := d.sched.Reload(desired); err != nil {
		return err
	}

	for name := range known {
		if !names[name] {
			slog.Info("daemon: deleting removed timer state", "unit", name)
			if err := d.store.DeleteTimerState(name); err != nil {
				slog.Warn("daemon: delete timer state failed", "unit", name, "error", err)
			}
		}
	}
	slog.Info("daemon: timers reloaded", "count", len(desired))
	return nil
}
