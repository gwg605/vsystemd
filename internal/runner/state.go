package runner

import (
	"encoding/json"
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
		event.Timestamp = time.Now()
	}

	r.mu.Lock()
	r.state = event
	r.mu.Unlock()

	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return r.transport.Send(daemonPeer, transport.Message{
		Type:    MessageStateEvent,
		Payload: payload,
	})
}

func (r *StateReporter) State() StateEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *StateReporter) Handle(sourcePeer string, msg transport.Message) error {
	if msg.Type != MessageStatusRequest {
		return nil
	}

	payload, err := json.Marshal(r.State())
	if err != nil {
		return err
	}
	return r.transport.Send(sourcePeer, transport.Message{
		Type:      MessageStateEvent,
		RequestID: msg.ID,
		Payload:   payload,
	})
}
