package runner

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gwg605/vsystemd/internal/transport"
)

const (
	daemonPeer = "daemon"

	MessageStateEvent    = "state_event"
	MessageStatusRequest = "status_request"
)

const (
	StateActivating = "activating"
	StateActive     = "active"
	StateInactive   = "inactive"
	StateFailed     = "failed"
	StateRestarting = "restarting"
)

type StateEvent struct {
	Unit         string    `json:"unit"`
	State        string    `json:"state"`
	PID          uint32    `json:"pid"`
	ExitCode     int       `json:"exit_code"`
	RestartCount int       `json:"restart_count"`
	Timestamp    time.Time `json:"timestamp"`
	LastError    string    `json:"last_error"`
}

type StateReporter struct {
	transport transport.Transport
	unit      string

	mu    sync.Mutex
	state StateEvent
}

func NewStateReporter(tr transport.Transport, unit string) *StateReporter {
	return &StateReporter{
		transport: tr,
		unit:      unit,
		state:     StateEvent{Unit: unit, State: StateInactive},
	}
}

func (r *StateReporter) Report(event StateEvent) error {
	if event.Unit == "" {
		event.Unit = r.unit
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	r.mu.Lock()
	r.state = event
	r.mu.Unlock()

	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("runner: encode state event failed", "error", err)
		return err
	}
	slog.Debug("runner: reporting state",
		"unit", event.Unit,
		"state", event.State,
		"pid", event.PID,
		"exit_code", event.ExitCode,
		"restart_count", event.RestartCount)
	if err := r.transport.Send(r.transport.Target(transport.PeerDaemon), transport.Message{
		Type:    MessageStateEvent,
		Payload: payload,
	}); err != nil {
		slog.Warn("runner: report state failed", "unit", event.Unit, "state", event.State, "error", err)
		return err
	}
	return nil
}

func (r *StateReporter) State() StateEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *StateReporter) Handle(source transport.Peer, msg transport.Message) error {
	if source.Type() != transport.PeerDaemon {
		return fmt.Errorf("forbidden: %s is not the daemon", source)
	}
	if msg.Type != MessageStatusRequest {
		return nil
	}
	slog.Debug("runner: status request received", "source", source.String())

	payload, err := json.Marshal(r.State())
	if err != nil {
		slog.Error("runner: encode status response failed", "error", err)
		return err
	}
	if err := r.transport.Send(source, transport.Message{
		Type:      MessageStateEvent,
		RequestID: msg.ID,
		Payload:   payload,
	}); err != nil {
		slog.Warn("runner: status response failed", "source", source.String(), "error", err)
		return err
	}
	return nil
}
