package fake

import (
	"fmt"
	"sort"
	"sync"

	"github.com/gwg605/vsystemd/internal/scm"
)

type service struct {
	config scm.ServiceConfig
	status scm.ServiceStatus
}

type Manager struct {
	mu              sync.Mutex
	services        map[string]*service
	processServices map[scm.ProcessHandle]string
	nextPID         uint32
}

var _ scm.Manager = (*Manager)(nil)

func New() *Manager {
	return &Manager{
		services:        make(map[string]*service),
		processServices: make(map[scm.ProcessHandle]string),
		nextPID:         1000,
	}
}

func (m *Manager) CreateService(cfg scm.ServiceConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.services[cfg.Name]; ok {
		return fmt.Errorf("create service %s: already exists", cfg.Name)
	}
	m.services[cfg.Name] = &service{
		config: cfg,
		status: scm.ServiceStatus{Name: cfg.Name, State: scm.StateStopped},
	}
	return nil
}

func (m *Manager) UpdateService(cfg scm.ServiceConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	svc, ok := m.services[cfg.Name]
	if !ok {
		return fmt.Errorf("update service %s: %w", cfg.Name, scm.ErrNotFound)
	}
	startType := svc.config.StartType
	svc.config = cfg
	if cfg.StartType == scm.StartTypeNoChange {
		svc.config.StartType = startType
	}
	return nil
}

func (m *Manager) Config(name string) (scm.ServiceConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	svc, ok := m.services[name]
	if !ok {
		return scm.ServiceConfig{}, fmt.Errorf("get config %s: %w", name, scm.ErrNotFound)
	}
	return svc.config, nil
}

func (m *Manager) SetStartType(name string, startType scm.StartType) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	svc, ok := m.services[name]
	if !ok {
		return fmt.Errorf("set start type %s: %w", name, scm.ErrNotFound)
	}
	svc.config.StartType = startType
	return nil
}

func (m *Manager) DeleteService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.services[name]; !ok {
		return fmt.Errorf("delete service %s: %w", name, scm.ErrNotFound)
	}
	delete(m.services, name)
	return nil
}

func (m *Manager) StartService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	svc, ok := m.services[name]
	if !ok {
		return fmt.Errorf("start service %s: %w", name, scm.ErrNotFound)
	}
	if svc.status.State == scm.StateRunning {
		return nil
	}
	m.nextPID++
	svc.status.State = scm.StateRunning
	svc.status.PID = m.nextPID
	svc.status.ExitCode = 0
	return nil
}

func (m *Manager) StopService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	svc, ok := m.services[name]
	if !ok {
		return fmt.Errorf("stop service %s: %w", name, scm.ErrNotFound)
	}
	if svc.status.State == scm.StateStopped {
		return nil
	}
	svc.status.State = scm.StateStopped
	svc.status.PID = 0
	svc.status.ExitCode = 0
	return nil
}

func (m *Manager) QueryStatus(name string) (scm.ServiceStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	svc, ok := m.services[name]
	if !ok {
		return scm.ServiceStatus{}, fmt.Errorf("query service %s: %w", name, scm.ErrNotFound)
	}
	return svc.status, nil
}

func (m *Manager) QueryStatuses() ([]scm.ServiceStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	statuses := make([]scm.ServiceStatus, 0, len(m.services))
	for _, svc := range m.services {
		statuses = append(statuses, svc.status)
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Name < statuses[j].Name
	})
	return statuses, nil
}

func (m *Manager) ListServices() ([]scm.ServiceConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	configs := make([]scm.ServiceConfig, 0, len(m.services))
	for _, svc := range m.services {
		configs = append(configs, svc.config)
	}
	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Name < configs[j].Name
	})
	return configs, nil
}

func (m *Manager) GetServiceByHandle(handle scm.ProcessHandle) (scm.ServiceConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name, ok := m.processServices[handle]
	if !ok {
		return scm.ServiceConfig{}, scm.ErrNotService
	}
	svc, ok := m.services[name]
	if !ok {
		return scm.ServiceConfig{}, scm.ErrNotService
	}
	return svc.config, nil
}

// SetProcessService registers a process handle as belonging to a service so
// that GetServiceByHandle can resolve it.
func (m *Manager) SetProcessService(handle scm.ProcessHandle, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processServices[handle] = name
}
