package unit

import "time"

type RestartPolicy string

const (
	RestartNo        RestartPolicy = "no"
	RestartAlways    RestartPolicy = "always"
	RestartOnFailure RestartPolicy = "on-failure"
)

type UnitFile struct {
	Name        ID
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
	LogsDirectory         []string
	StartLimitBurst       int
	StartLimitIntervalSec time.Duration
}

type TimerUnit struct {
	OnCalendar   []string
	OnBootSec    time.Duration
	OnStartupSec time.Duration
	Unit         string
}

// Dependencies returns the service units referenced by After=/Requires=,
// de-duplicated and in declaration order.
func (u *UnitFile) Dependencies() []string {
	seen := make(map[string]struct{})
	var dependencies []string
	for _, names := range [][]string{u.Requires, u.After} {
		for _, name := range names {
			id, err := ParseID(name)
			if err != nil || id.Type != TypeService {
				continue
			}
			if _, ok := seen[id.Name]; ok {
				continue
			}
			seen[id.Name] = struct{}{}
			dependencies = append(dependencies, id.Name)
		}
	}
	return dependencies
}
