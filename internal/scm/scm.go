package scm

import (
	"errors"

	"github.com/gwg605/vsystemd/internal/unit"
)

type StartType int

// StartTypeNoChange leaves the service start type unchanged on update.
const StartTypeNoChange StartType = 0

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
)

// ProcessHandle is an opaque OS handle to a process (a Windows HANDLE).
type ProcessHandle uintptr

var (
	// ErrNotFound reports that a service does not exist.
	ErrNotFound = errors.New("service not found")
	// ErrNotService reports that a process is not a service at all.
	ErrNotService = errors.New("process is not a service")
	// ErrNotManaged reports that a service was not created by vsystemd.
	ErrNotManaged = errors.New("service is not managed by vsystemd")
)

// Account is an account a service runs under.
type Account string

// NewAccount resolves the win32 service account for a unit's User= value,
// defaulting to the per-service virtual account NT SERVICE\<name>.
func NewAccount(user, serviceName string) Account {
	if user != "" {
		return Account(user)
	}
	return Account(`NT SERVICE\` + serviceName)
}

type ServiceConfig struct {
	Name         string
	BinaryPath   string
	StartType    StartType
	Dependencies []string
	User         Account
	password     string
	DisplayName  string
	Description  string
}

// SetPassword sets the service account password. The password is never exposed
// by read operations; only the scm package reads it.
func (c *ServiceConfig) SetPassword(password string) {
	c.password = password
}

type ServiceStatus struct {
	Name     string
	State    State
	PID      uint32
	ExitCode uint32
}

type Manager interface {
	CreateService(cfg ServiceConfig, content unit.Content) error
	UpdateService(cfg ServiceConfig, content unit.Content) error
	// SetStartType changes only the start type of an existing managed service.
	SetStartType(name string, startType StartType) error
	DeleteService(name string) error
	StartService(name string) error
	StopService(name string) error
	QueryStatus(name string) (ServiceStatus, error)
	QueryStatuses() ([]ServiceStatus, error)
	// ListServices returns the configuration of every managed service. The
	// password is never populated because SCM does not expose it.
	ListServices() ([]ServiceConfig, error)
	// GetService returns the configuration of a single managed service.
	GetService(name string) (ServiceConfig, error)
	// GetServiceByHandle returns the configuration of the managed service the
	// process handle belongs to, or ErrNotService if it is not a service.
	GetServiceByHandle(handle ProcessHandle) (ServiceConfig, error)
	// LoadUnitContent returns the applied unit snapshot stored in the service's
	// registry Parameters key, or an error if it is missing.
	LoadUnitContent(name string) (unit.Content, error)
}

// ContentReader reads the applied unit snapshot of a service.
type ContentReader interface {
	LoadUnitContent(name string) (unit.Content, error)
}
