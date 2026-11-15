package daemon

import (
	"encoding/json"
	"errors"
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

func (d *Daemon) handle(source transport.Peer, msg transport.Message) error {
	slog.Debug("daemon: message received", "from", source.String(), "type", msg.Type)
	switch msg.Type {
	case runner.MessageStateEvent:
		return d.handleStateEvent(source, msg)
	case protocol.TypeStatusRequest:
		if err := authorizeControl(source); err != nil {
			return d.respondError(source, msg.ID, err)
		}
		return d.handleStatusRequest(source, msg)
	case protocol.TypeDaemonReload:
		if err := authorizeControl(source); err != nil {
			return d.respondError(source, msg.ID, err)
		}
		return d.handleDaemonReload(source, msg)
	case protocol.TypeListUnits:
		if err := authorizeControl(source); err != nil {
			return d.respondError(source, msg.ID, err)
		}
		return d.handleListUnits(source, msg)
	case protocol.TypeTimerEnable:
		if err := authorizeControl(source); err != nil {
			return d.respondError(source, msg.ID, err)
		}
		return d.handleTimerEnable(source, msg)
	case protocol.TypeTimerStart:
		if err := authorizeControl(source); err != nil {
			return d.respondError(source, msg.ID, err)
		}
		return d.handleTimerStart(source, msg)
	case protocol.TypeTimerStop:
		if err := authorizeControl(source); err != nil {
			return d.respondError(source, msg.ID, err)
		}
		return d.handleTimerStop(source, msg)
	default:
		return nil
	}
}

// authorizeControl requires a control peer with administrative rights.
func authorizeControl(source transport.Peer) error {
	if source.Type() != transport.PeerControl {
		return fmt.Errorf("forbidden: %s is not a control peer", source)
	}
	if !source.Elevated() {
		return errors.New("forbidden: control operation requires elevation")
	}
	return nil
}

func (d *Daemon) handleStateEvent(source transport.Peer, msg transport.Message) error {
	if source.Type() != transport.PeerRunner {
		return fmt.Errorf("forbidden: state event from %s", source)
	}
	var event runner.StateEvent
	if err := json.Unmarshal(msg.Payload, &event); err != nil {
		slog.Error("daemon: decode state event failed", "from", source.String(), "error", err)
		return fmt.Errorf("decode state event: %w", err)
	}
	if event.Unit == "" {
		event.Unit = source.Name()
	}
	if event.Unit != source.Name() {
		return fmt.Errorf("forbidden: state event for %q from %q", event.Unit, source.Name())
	}
	d.updateFromEvent(event)
	return nil
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

			response, err := d.transport.Req(
				d.transport.Target(transport.PeerRunner, transport.WithName(unit)),
				transport.Message{Type: runner.MessageStatusRequest},
			)
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
		transition = time.Now().UTC()
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
