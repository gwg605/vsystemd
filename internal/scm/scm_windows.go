//go:build windows

package scm

import (
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const managedServiceGroup = "vsystemd"

type windowsManager struct{}

func New() Manager {
	return windowsManager{}
}

func (windowsManager) CreateService(cfg ServiceConfig) error {
	executable, args, err := splitBinaryPath(cfg.BinaryPath)
	if err != nil {
		return fmt.Errorf("parse binary path for %s: %w", cfg.Name, err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	service, err := m.CreateService(cfg.Name, executable, windowsServiceConfig(cfg), args...)
	if err != nil {
		return fmt.Errorf("create service %s: %w", cfg.Name, err)
	}
	return service.Close()
}

func (windowsManager) UpdateService(cfg ServiceConfig) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	service, err := m.OpenService(cfg.Name)
	if err != nil {
		return fmt.Errorf("open service %s: %w", cfg.Name, err)
	}
	defer service.Close()

	config := windowsServiceConfig(cfg)
	if cfg.StartType == 0 {
		config.StartType = windows.SERVICE_NO_CHANGE
	}
	if err := service.UpdateConfig(config); err != nil {
		return fmt.Errorf("update service %s: %w", cfg.Name, err)
	}
	return nil
}

func (windowsManager) SetStartType(name string, startType StartType) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	service, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("open service %s: %w", name, err)
	}
	defer service.Close()

	err = windows.ChangeServiceConfig(
		service.Handle,
		windows.SERVICE_NO_CHANGE,
		windowsStartType(startType),
		windows.SERVICE_NO_CHANGE,
		nil, nil, nil, nil, nil, nil, nil,
	)
	if err != nil {
		return fmt.Errorf("set start type for %s: %w", name, err)
	}
	return nil
}

func (windowsManager) DeleteService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	service, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("open service %s: %w", name, err)
	}
	defer service.Close()

	if err := service.Delete(); err != nil {
		return fmt.Errorf("delete service %s: %w", name, err)
	}
	return nil
}

func (windowsManager) StartService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	service, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("open service %s: %w", name, err)
	}
	defer service.Close()

	status, err := service.Query()
	if err != nil {
		return fmt.Errorf("query service %s: %w", name, err)
	}
	if mapState(status.State) == StateRunning {
		return nil
	}
	if err := service.Start(); err != nil {
		return fmt.Errorf("start service %s: %w", name, err)
	}
	return nil
}

func (windowsManager) StopService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	service, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("open service %s: %w", name, err)
	}
	defer service.Close()

	status, err := service.Query()
	if err != nil {
		return fmt.Errorf("query service %s: %w", name, err)
	}
	if mapState(status.State) == StateStopped {
		return nil
	}
	if _, err := service.Control(svc.Stop); err != nil {
		return fmt.Errorf("stop service %s: %w", name, err)
	}
	return nil
}

func (windowsManager) QueryStatus(name string) (ServiceStatus, error) {
	m, err := mgr.Connect()
	if err != nil {
		return ServiceStatus{}, fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()
	return queryService(m, name)
}

func (windowsManager) ListServices() ([]ServiceStatus, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	names, err := m.ListServices()
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}

	statuses := make([]ServiceStatus, 0, len(names))
	for _, name := range names {
		service, err := m.OpenService(name)
		if err != nil {
			continue
		}
		config, err := service.Config()
		service.Close()
		if err != nil || config.LoadOrderGroup != managedServiceGroup {
			continue
		}

		status, err := queryService(m, name)
		if err != nil {
			continue
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func queryService(m *mgr.Mgr, name string) (ServiceStatus, error) {
	service, err := m.OpenService(name)
	if err != nil {
		return ServiceStatus{}, fmt.Errorf("open service %s: %w", name, err)
	}
	defer service.Close()

	status, err := service.Query()
	if err != nil {
		return ServiceStatus{}, fmt.Errorf("query service %s: %w", name, err)
	}
	return ServiceStatus{
		Name:     name,
		State:    mapState(status.State),
		PID:      status.ProcessId,
		ExitCode: serviceExitCode(status),
	}, nil
}

func splitBinaryPath(binaryPath string) (string, []string, error) {
	parts, err := windows.DecomposeCommandLine(binaryPath)
	if err != nil {
		return "", nil, err
	}
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("empty binary path")
	}
	return parts[0], parts[1:], nil
}

func windowsServiceConfig(cfg ServiceConfig) mgr.Config {
	return mgr.Config{
		ServiceType:      windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:        windowsStartType(cfg.StartType),
		ErrorControl:     windows.SERVICE_ERROR_NORMAL,
		BinaryPathName:   cfg.BinaryPath,
		LoadOrderGroup:   managedServiceGroup,
		Dependencies:     cfg.Dependencies,
		ServiceStartName: cfg.User,
		DisplayName:      cfg.Name,
		Password:         cfg.Password,
		Description:      cfg.Description,
	}
}

func windowsStartType(t StartType) uint32 {
	switch t {
	case StartTypeBoot:
		return windows.SERVICE_BOOT_START
	case StartTypeSystem:
		return windows.SERVICE_SYSTEM_START
	case StartTypeAutomatic:
		return windows.SERVICE_AUTO_START
	case StartTypeDisabled:
		return windows.SERVICE_DISABLED
	default:
		return windows.SERVICE_DEMAND_START
	}
}

func mapState(state svc.State) State {
	switch state {
	case svc.Stopped:
		return StateStopped
	case svc.StartPending:
		return StateStartPending
	case svc.StopPending:
		return StateStopPending
	case svc.Running:
		return StateRunning
	case svc.ContinuePending:
		return StateContinuePending
	case svc.PausePending:
		return StatePausePending
	case svc.Paused:
		return StatePaused
	default:
		return State(state)
	}
}

func serviceExitCode(status svc.Status) uint32 {
	if status.Win32ExitCode == uint32(windows.ERROR_SERVICE_SPECIFIC_ERROR) {
		return status.ServiceSpecificExitCode
	}
	return status.Win32ExitCode
}
