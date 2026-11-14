package unit

import "time"

type RestartPolicy string

const (
	RestartNo        RestartPolicy = "no"
	RestartAlways    RestartPolicy = "always"
	RestartOnFailure RestartPolicy = "on-failure"
)

type UnitFile struct {
	Name        string
	Description string
	After       []string
	Requires    []string
	Wants       []string
	Service     *ServiceUnit
	Timer       *TimerUnit
	Warnings    []string
}

type ServiceUnit struct {
	ExecStart             string
	ExecStartPre          []string
	ExecStartPost         []string
	ExecStop              string
	Restart               RestartPolicy
	RestartSec            time.Duration
	TimeoutStopSec        time.Duration
	WorkingDirectory      string
	Environment           map[string]string
	EnvironmentFiles      []string
	User                  string
	StandardOutput        string
	StandardError         string
	StartLimitBurst       int
	StartLimitIntervalSec time.Duration
}

type TimerUnit struct {
	OnCalendar   []string
	OnBootSec    time.Duration
	OnStartupSec time.Duration
	Unit         string
}
