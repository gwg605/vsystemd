package protocol

import (
	"time"

	"github.com/gwg605/vsystemd/internal/scm"
)

const (
	TypeStatusRequest     = "status_request"
	TypeStatusResponse    = "status_response"
	TypeListUnits         = "list_units"
	TypeListUnitsResponse = "list_units_response"
	TypeDaemonReload      = "daemon_reload"
	TypeReloadResult      = "reload_result"
	TypeErrorResponse     = "error_response"
	TypeTimerEnable       = "timer_enable"
	TypeTimerStart        = "timer_start"
	TypeTimerStop         = "timer_stop"
	TypeTimerResult       = "timer_result"
)

type StatusRequest struct {
	Unit string `json:"unit"`
}

type StatusResponse struct {
	Unit           string     `json:"unit"`
	SystemdState   string     `json:"systemd_state"`
	SCMState       scm.State  `json:"scm_state"`
	PID            uint32     `json:"pid"`
	ExitCode       int        `json:"exit_code"`
	RestartCount   int        `json:"restart_count"`
	LastTransition time.Time  `json:"last_transition"`
	LastError      string     `json:"last_error"`
	LogTail        []string   `json:"log_tail"`
	Description    string     `json:"description,omitempty"`
	Enabled        *bool      `json:"enabled,omitempty"`
	Running        *bool      `json:"running,omitempty"`
	NextRun        *time.Time `json:"next_run,omitempty"`
	LastRun        *time.Time `json:"last_run,omitempty"`
	ActiveSince    *time.Time `json:"active_since,omitempty"`
	Activates      string     `json:"activates,omitempty"`
}

type UnitStatus struct {
	Unit         string     `json:"unit"`
	Type         string     `json:"type"`
	SystemdState string     `json:"systemd_state"`
	SCMState     scm.State  `json:"scm_state"`
	Description  string     `json:"description"`
	Enabled      *bool      `json:"enabled"`
	Running      *bool      `json:"running"`
	NextRun      *time.Time `json:"next_run"`
	LastRun      *time.Time `json:"last_run"`
	Activates    string     `json:"activates,omitempty"`
}

type ListUnitsResponse struct {
	Units []UnitStatus `json:"units"`
}

type ReloadResult struct {
	Success bool `json:"success"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type TimerRequest struct {
	Unit    string `json:"unit"`
	Enabled bool   `json:"enabled"`
}

type TimerResult struct {
	Unit    string    `json:"unit"`
	Success bool      `json:"success"`
	Enabled bool      `json:"enabled"`
	Running bool      `json:"running"`
	LastRun time.Time `json:"last_run"`
	NextRun time.Time `json:"next_run"`
}
