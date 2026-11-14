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
	mu       sync.Mutex
	services map[string]*service
	nextPID  uint32
}

var _ scm.Manager = (*Manager)(nil)

func New() *Manager {
	return &Manager{
		services: make(map[string]*service),
		nextPID:  1000,
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
		return fmt.Errorf("update service %s: not found", cfg.Name)
	}
	startType := svc.config.StartType
	svc.config = cfg
	if cfg.StartType == 0 {
		svc.config.StartType = startType
	}
	return nil
}

func (m *Manager) Config(name string) (scm.ServiceConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	svc, ok := m.services[name]
	if !ok {
		return scm.ServiceConfig{}, fmt.Errorf("get config %s: not found", name)
	}
	return svc.config, nil
}

func (m *Manager) DeleteService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.services[name]; !ok {
		return fmt.Errorf("delete service %s: not found", name)
	}
	delete(m.services, name)
	return nil
}

func (m *Manager) StartService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	svc, ok := m.services[name]
	if !ok {
		return fmt.Errorf("start service %s: not found", name)
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
		return fmt.Errorf("stop service %s: not found", name)
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
		return scm.ServiceStatus{}, fmt.Errorf("query service %s: not found", name)
	}
	return svc.status, nil
}

func (m *Manager) ListServices() ([]scm.ServiceStatus, error) {
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
