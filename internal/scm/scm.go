package scm

type StartType int

const (
	StartTypeAutomatic StartType = iota + 1
	StartTypeManual
	StartTypeDisabled
	StartTypeBoot
	StartTypeSystem
)

type State uint32

const (
	StateStopped State = iota + 1
	StateStartPending
	StateStopPending
	StateRunning
	StateContinuePending
	StatePausePending
	StatePaused
)

type ServiceConfig struct {
	Name         string
	BinaryPath   string
	StartType    StartType
	Dependencies []string
	User         string
	Password     string
	Description  string
}

type ServiceStatus struct {
	Name     string
	State    State
	PID      uint32
	ExitCode uint32
}

type Manager interface {
	CreateService(cfg ServiceConfig) error
	UpdateService(cfg ServiceConfig) error
	SetStartType(name string, startType StartType) error
	DeleteService(name string) error
	StartService(name string) error
	StopService(name string) error
	QueryStatus(name string) (ServiceStatus, error)
	ListServices() ([]ServiceStatus, error)
}
