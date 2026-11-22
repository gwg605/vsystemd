package protocol

import "time"

const MessageStateEvent = "state_event"

type State string

const (
	StateActivating   State = "activating"
	StateActive       State = "active"
	StateInactive     State = "inactive"
	StateFailed       State = "failed"
	StateRestarting   State = "restarting"
	StateDeactivating State = "deactivating"
)

type StateEvent struct {
	Unit         string    `json:"unit"`
	State        State     `json:"state"`
	PID          uint32    `json:"pid"`
	ExitCode     int       `json:"exit_code"`
	RestartCount int       `json:"restart_count"`
	Timestamp    time.Time `json:"timestamp"`
	LastError    string    `json:"last_error"`
	LoadedHash   string    `json:"loaded_hash,omitempty"`
}
