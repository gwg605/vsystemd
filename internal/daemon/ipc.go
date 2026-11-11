package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/runner"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/transport"
)

func (d *Daemon) handle(sourcePeer string, msg transport.Message) error {
	slog.Debug("daemon: message received", "from", sourcePeer, "type", msg.Type)
	switch msg.Type {
	case runner.MessageStateEvent:
		var event runner.StateEvent
		if err := json.Unmarshal(msg.Payload, &event); err != nil {
			slog.Error("daemon: decode state event failed", "from", sourcePeer, "error", err)
			return fmt.Errorf("decode state event: %w", err)
		}
		if event.Unit == "" {
			event.Unit = sourcePeer
		}
		d.updateFromEvent(event)
		return nil
	case protocol.TypeStatusRequest:
		return d.handleStatusRequest(sourcePeer, msg)
	case protocol.TypeDaemonReload:
		return d.handleDaemonReload(sourcePeer, msg)
	case protocol.TypeListUnits:
		return d.handleListUnits(sourcePeer, msg)
	case protocol.TypeTimerEnable:
		return d.handleTimerEnable(sourcePeer, msg)
	case protocol.TypeTimerStart:
		return d.handleTimerStart(sourcePeer, msg)
	case protocol.TypeTimerStop:
		return d.handleTimerStop(sourcePeer, msg)
	default:
		return nil
	}
}

func (d *Daemon) requestRunnerStates() error {
	slog.Info("daemon: requesting runner states")
	units, err := d.store.ListUnits()
	if err != nil {
		return fmt.Errorf("list units: %w", err)
	}

	var wg sync.WaitGroup
	for _, unit := range units {
		if !strings.HasSuffix(unit, ".service") {
			continue
		}
		wg.Add(1)
		go func(unit string) {
			defer wg.Done()

			response, err := d.transport.Req(unit, transport.Message{
				Type: runner.MessageStatusRequest,
			})
			if err != nil {
				slog.Debug("daemon: runner state request failed", "unit", unit, "error", err)
				return
			}

			var event runner.StateEvent
			if err := json.Unmarshal(response.Payload, &event); err != nil {
				slog.Warn("daemon: decode runner state response failed", "unit", unit, "error", err)
				return
			}
			if event.Unit == "" {
				event.Unit = unit
			}
			d.updateFromEvent(event)
		}(unit)
	}
	wg.Wait()
	slog.Info("daemon: runner states requested")
	return nil
}

func (d *Daemon) State(unit string) (ServiceState, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	state, ok := d.states[unit]
	return state, ok
}

func (d *Daemon) updateFromEvent(event runner.StateEvent) {
	if event.Unit == "" {
		return
	}

	transition := event.Timestamp
	if transition.IsZero() {
		transition = time.Now()
	}

	state := ServiceState{
		Unit:           event.Unit,
		SCMState:       d.scmState(event.Unit),
		RunnerState:    RunnerState(event.State),
		PID:            event.PID,
		ExitCode:       event.ExitCode,
		RestartCount:   event.RestartCount,
		LastTransition: transition,
		LastError:      event.LastError,
	}

	d.mu.Lock()
	d.states[event.Unit] = state
	d.mu.Unlock()

	slog.Debug("daemon: service state updated",
		"unit", event.Unit,
		"runner_state", event.State,
		"scm_state", state.SCMState,
		"pid", state.PID,
		"exit_code", state.ExitCode)
}

func (d *Daemon) scmState(unit string) scm.State {
	name := strings.TrimSuffix(unit, ".service")
	status, err := d.manager.QueryStatus(name)
	if err != nil {
		return 0
	}
	return status.State
}
