package daemon

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

func (d *Daemon) handle(source transport.Peer, msg transport.Message) error {
	slog.Debug("daemon: message received", "from", source.String(), "type", msg.Type)
	switch msg.Type {
	case protocol.MessageStateEvent:
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
	case protocol.TypeVerifyUnitFile:
		if err := authorizeControl(source); err != nil {
			return d.respondError(source, msg.ID, err)
		}
		return d.handleVerifyUnitFile(source, msg)
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
	var event protocol.StateEvent
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
// User= declared by its unit. A unit that is unknown to the daemon is rejected.
func (d *Daemon) verifyRunnerAccount(source transport.Peer) error {
	account := source.ServiceConfig().User
	if account == "" {
		return nil
	}
	id, err := unit.ParseID(source.Name())
	if err != nil {
		return fmt.Errorf("forbidden: state event from %s with invalid unit name", source)
	}
	entry, ok := d.units.entry(id)
	if !ok || entry.parsed == nil || entry.parsed.Service == nil {
		return fmt.Errorf("forbidden: state event from %s for unknown unit", source)
	}
	expected := scm.NewAccount(entry.parsed.Service.User, id.Name)
	if !strings.EqualFold(string(account), string(expected)) {
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

func (d *Daemon) updateFromEvent(event protocol.StateEvent) {
	if event.Unit == "" {
		return
	}
	id, err := unit.ParseID(event.Unit)
	if err != nil {
		return
	}

	transition := event.Timestamp
	if transition.IsZero() {
		transition = time.Now().UTC()
	}

	d.mu.Lock()
	previous, had := d.states[id.String()]
	state := ServiceState{
		Unit:           id.String(),
		RunnerState:    RunnerState(event.State),
		PID:            event.PID,
		ExitCode:       event.ExitCode,
		RestartCount:   event.RestartCount,
		LastTransition: transition,
		LastSeen:       time.Now().UTC(),
		LastError:      event.LastError,
	}
	if had && previous.RunnerState == state.RunnerState {
		state.LastTransition = previous.LastTransition
	}
	d.states[id.String()] = state
	d.mu.Unlock()

	slog.Debug("daemon: service state updated",
		"unit", id,
		"runner_state", event.State,
		"pid", state.PID,
		"exit_code", state.ExitCode)
}

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
