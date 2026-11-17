package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/runner"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
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
	if err := d.verifyRunnerAccount(source); err != nil {
		return err
	}
	d.updateFromEvent(event)
	return nil
}

// verifyRunnerAccount checks that the account the service runs under matches the
// User= declared by its unit. A missing unit or config is not an error here.
func (d *Daemon) verifyRunnerAccount(source transport.Peer) error {
	account := source.ServiceConfig().User
	if account == "" {
		return nil
	}
	id, err := unit.ParseID(source.Name())
	if err != nil {
		return nil
	}
	data, err := d.store.ReadUnit(id.String())
	if err != nil {
		return nil
	}
	parsed, err := unit.Parse(id, data)
	if err != nil || parsed.Service == nil {
		return nil
	}
	expected := unitUser(parsed.Service.User, id.Name)
	if !strings.EqualFold(account, expected) {
		return fmt.Errorf("forbidden: state event from %s with unexpected account %q", source, account)
	}
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
