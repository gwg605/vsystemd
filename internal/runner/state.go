package runner

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/transport"
)

const daemonPeer = "daemon"

type stateReporter struct {
	transport transport.Transport
	unit      string

	mu    sync.Mutex
	state protocol.StateEvent
}

func newStateReporter(tr transport.Transport, unit string) *stateReporter {
	return &stateReporter{
		transport: tr,
		unit:      unit,
		state:     protocol.StateEvent{Unit: unit, State: protocol.StateInactive},
	}
}

func (r *stateReporter) Report(event protocol.StateEvent) error {
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
		Type:    protocol.MessageStateEvent,
		Payload: payload,
	}); err != nil {
		slog.Warn("runner: report state failed", "unit", event.Unit, "state", event.State, "error", err)
		return err
	}
	return nil
}

func (r *stateReporter) State() protocol.StateEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// Heartbeat re-sends the last reported state with a fresh timestamp so the
// daemon knows the runner is still alive.
func (r *stateReporter) Heartbeat() error {
	state := r.State()
	state.Timestamp = time.Time{}
	return r.Report(state)
}
