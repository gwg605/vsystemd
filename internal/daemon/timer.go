package daemon

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/scheduler"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

func (d *Daemon) handleTimerEnable(sourcePeer string, msg transport.Message) error {
	var request protocol.TimerRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	if err := d.validateTimer(request.Unit); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}

	state, err := d.store.ReadTimerState(request.Unit)
	if err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	state.Enabled = request.Enabled
	if err := d.store.WriteTimerState(request.Unit, state); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}

	response, err := d.timerResult(request.Unit)
	if err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	return d.respond(sourcePeer, protocol.TypeTimerResult, msg.ID, response)
}

func (d *Daemon) handleTimerStart(sourcePeer string, msg transport.Message) error {
	var request protocol.TimerRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}

	definition, err := d.timerDefinition(request.Unit)
	if err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	if !d.sched.Has(definition.Name) {
		if err := d.sched.Add(definition); err != nil {
			return d.respondError(sourcePeer, msg.ID, err)
		}
	}

	response, err := d.timerResult(request.Unit)
	if err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	return d.respond(sourcePeer, protocol.TypeTimerResult, msg.ID, response)
}

func (d *Daemon) handleTimerStop(sourcePeer string, msg transport.Message) error {
	var request protocol.TimerRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	if err := d.validateTimer(request.Unit); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}

	if err := d.sched.Stop(request.Unit); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}

	response, err := d.timerResult(request.Unit)
	if err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	return d.respond(sourcePeer, protocol.TypeTimerResult, msg.ID, response)
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

func (d *Daemon) validateTimer(name string) error {
	if !strings.HasSuffix(name, ".timer") {
		return fmt.Errorf("unit %s is not a timer", name)
	}
	data, err := d.store.ReadUnit(name)
	if err != nil {
		return fmt.Errorf("timer %s not found", name)
	}
	if _, err := unit.Parse(name, data); err != nil {
		return fmt.Errorf("parse timer %s: %w", name, err)
	}
	return nil
}

func (d *Daemon) timerDefinition(name string) (scheduler.Timer, error) {
	if !strings.HasSuffix(name, ".timer") {
		return scheduler.Timer{}, fmt.Errorf("unit %s is not a timer", name)
	}
	data, err := d.store.ReadUnit(name)
	if err != nil {
		return scheduler.Timer{}, fmt.Errorf("timer %s not found", name)
	}
	parsed, err := unit.Parse(name, data)
	if err != nil {
		return scheduler.Timer{}, fmt.Errorf("parse timer %s: %w", name, err)
	}
	if len(parsed.Timer.OnCalendar) == 0 {
		return scheduler.Timer{}, fmt.Errorf("timer %s has no OnCalendar", name)
	}

	target := parsed.Timer.Unit
	if target == "" {
		target = strings.TrimSuffix(name, ".timer") + ".service"
	}
	return scheduler.Timer{
		Name:       name,
		Service:    strings.TrimSuffix(target, filepath.Ext(target)),
		OnCalendar: parsed.Timer.OnCalendar,
	}, nil
}

func (d *Daemon) startTimers() error {
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
		known[name] = true
		if !states[name].Enabled {
			continue
		}
		definition, err := d.timerDefinition(name)
		if err != nil {
			continue
		}
		if err := d.sched.Add(definition); err != nil {
			continue
		}
	}

	d.timerMu.Lock()
	d.knownTimers = known
	d.timerMu.Unlock()
	return nil
}

func (d *Daemon) reloadTimers() error {
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
		names[name] = true
		definition, err := d.timerDefinition(name)
		if err != nil {
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
			_ = d.store.DeleteTimerState(name)
		}
	}
	return nil
}
