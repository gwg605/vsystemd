package runner

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gwg605/vsystemd/internal/transport"
)

const (
	daemonPeer = "daemon"

	MessageStateEvent = "state_event"
)

const (
	StateActivating   = "activating"
	StateActive       = "active"
	StateInactive     = "inactive"
	StateFailed       = "failed"
	StateRestarting   = "restarting"
	StateDeactivating = "deactivating"
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

// Heartbeat re-sends the last reported state with a fresh timestamp so the
// daemon knows the runner is still alive.
func (r *StateReporter) Heartbeat() error {
	state := r.State()
	state.Timestamp = time.Time{}
	return r.Report(state)
}
