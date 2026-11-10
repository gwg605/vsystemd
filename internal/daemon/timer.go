package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

const (
	messageTimerEnable = "timer_enable"
	messageTimerStart  = "timer_start"
	messageTimerStop   = "timer_stop"
	messageTimerResult = "timer_result"
)

type timerRequest struct {
	Unit    string `json:"unit"`
	Enabled bool   `json:"enabled"`
}

type timerResult struct {
	Unit    string    `json:"unit"`
	Success bool      `json:"success"`
	Enabled bool      `json:"enabled"`
	Running bool      `json:"running"`
	LastRun time.Time `json:"last_run"`
	NextRun time.Time `json:"next_run"`
}

type timerRuntime struct {
	running bool
}

func (d *Daemon) handleTimerEnable(sourcePeer string, msg transport.Message) error {
	var request timerRequest
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
	return d.respond(sourcePeer, messageTimerResult, msg.ID, response)
}

func (d *Daemon) handleTimerStart(sourcePeer string, msg transport.Message) error {
	var request timerRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	if err := d.validateTimer(request.Unit); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}

	d.setTimerRunning(request.Unit, true)
	response, err := d.timerResult(request.Unit)
	if err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	return d.respond(sourcePeer, messageTimerResult, msg.ID, response)
}

func (d *Daemon) handleTimerStop(sourcePeer string, msg transport.Message) error {
	var request timerRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	if err := d.validateTimer(request.Unit); err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}

	d.setTimerRunning(request.Unit, false)
	response, err := d.timerResult(request.Unit)
	if err != nil {
		return d.respondError(sourcePeer, msg.ID, err)
	}
	return d.respond(sourcePeer, messageTimerResult, msg.ID, response)
}

func (d *Daemon) timerResult(name string) (timerResult, error) {
	state, err := d.store.ReadTimerState(name)
	if err != nil {
		return timerResult{}, fmt.Errorf("read timer state %s: %w", name, err)
	}
	return timerResult{
		Unit:    name,
		Success: true,
		Enabled: state.Enabled,
		Running: d.timerRunning(name),
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

func (d *Daemon) startTimers() error {
	units, err := d.store.ListUnits()
	if err != nil {
		return fmt.Errorf("list units: %w", err)
	}
	states, err := d.store.ListTimerStates()
	if err != nil {
		return fmt.Errorf("list timer states: %w", err)
	}

	d.timerMu.Lock()
	defer d.timerMu.Unlock()
	for _, name := range units {
		if !strings.HasSuffix(name, ".timer") {
			continue
		}
		if states[name].Enabled {
			d.timers[name] = &timerRuntime{running: true}
		}
	}
	return nil
}

func (d *Daemon) setTimerRunning(name string, running bool) {
	d.timerMu.Lock()
	defer d.timerMu.Unlock()
	runtime := d.timers[name]
	if runtime == nil {
		runtime = &timerRuntime{}
		d.timers[name] = runtime
	}
	runtime.running = running
}

func (d *Daemon) timerRunning(name string) bool {
	d.timerMu.Lock()
	defer d.timerMu.Unlock()
	runtime := d.timers[name]
	return runtime != nil && runtime.running
}
