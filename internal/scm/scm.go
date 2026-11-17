package scm

import "errors"

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

// ProcessHandle is an opaque OS handle to a process (a Windows HANDLE).
type ProcessHandle uintptr

var (
	// ErrNotService reports that a process is not a service at all.
	ErrNotService = errors.New("process is not a service")
	// ErrNotManaged reports that a service was not created by vsystemd.
	ErrNotManaged = errors.New("service is not managed by vsystemd")
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
	DeleteService(name string) error
	StartService(name string) error
	StopService(name string) error
	QueryStatus(name string) (ServiceStatus, error)
	QueryStatuses() ([]ServiceStatus, error)
	// ListServices returns the configuration of every managed service. Password
	// is never populated because SCM does not expose it.
	ListServices() ([]ServiceConfig, error)
	// GetServiceByHandle returns the configuration of the managed service the
	// process handle belongs to, or ErrNotService if it is not a service.
	GetServiceByHandle(handle ProcessHandle) (ServiceConfig, error)
}