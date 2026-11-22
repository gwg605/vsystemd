//go:build windows

package scm

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/gwg605/vsystemd/internal/unit"
)

const (
	managedServiceGroup = "vsystemd"
	// maxSIDSize is SECURITY_MAX_SID_SIZE.
	maxSIDSize = 68
	// snapshotValue is the registry value holding the applied unit content.
	snapshotValue = "Unit"
)

type windowsManager struct{}

func New() Manager {
	return windowsManager{}
}

func (windowsManager) CreateService(cfg ServiceConfig, content unit.Content) error {
	executable, args, err := splitBinaryPath(cfg.BinaryPath)
	if err != nil {
		return fmt.Errorf("parse binary path for %s: %w", cfg.Name, err)
	}

	m, err := connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	service, err := m.CreateService(cfg.Name, executable, windowsServiceConfig(cfg), args...)
	if err != nil {
		return fmt.Errorf("create service %s: %w", cfg.Name, err)
	}
	closeErr := service.Close()
	if closeErr != nil {
		return fmt.Errorf("create service %s: %w", cfg.Name, closeErr)
	}
	if err := saveUnitContent(cfg.Name, content); err != nil {
		return err
	}
	return nil
}

func (windowsManager) UpdateService(cfg ServiceConfig, content unit.Content) error {
	m, err := connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	service, _, err := openManaged(m, cfg.Name)
	if err != nil {
		return err
	}
	defer service.Close()

	config := windowsServiceConfig(cfg)
	if cfg.StartType == StartTypeNoChange {
		config.StartType = windows.SERVICE_NO_CHANGE
	}
	if err := service.UpdateConfig(config); err != nil {
		return fmt.Errorf("update service %s: %w", cfg.Name, err)
	}
	if err := saveUnitContent(cfg.Name, content); err != nil {
		return err
	}
	return nil
}

func snapshotKeyPath(name string) string {
	return `SYSTEM\CurrentControlSet\Services\` + name + `\Parameters`
}

func saveUnitContent(name string, content unit.Content) error {
	if len(content) == 0 {
		return nil
	}
	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, snapshotKeyPath(name), registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open parameters key for %s: %w", name, err)
	}
	defer key.Close()
	if err := key.SetBinaryValue(snapshotValue, content); err != nil {
		return fmt.Errorf("write unit content for %s: %w", name, err)
	}
	return nil
}

func (windowsManager) LoadUnitContent(name string) (unit.Content, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, snapshotKeyPath(name), registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("open parameters key for %s: %w", name, err)
	}
	defer key.Close()
	value, _, err := key.GetBinaryValue(snapshotValue)
	if err != nil {
		return nil, fmt.Errorf("read unit content for %s: %w", name, err)
	}
	return unit.Content(value), nil
}

func (windowsManager) SetStartType(name string, startType StartType) error {
	m, err := connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	service, config, err := openManaged(m, name)
	if err != nil {
		return err
	}
	defer service.Close()

	config.StartType = windowsStartType(startType)
	if err := service.UpdateConfig(config); err != nil {
		return fmt.Errorf("set start type %s: %w", name, err)
	}
	return nil
}

func (windowsManager) DeleteService(name string) error {
	m, err := connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	service, _, err := openManaged(m, name)
	if err != nil {
		return err
	}
	defer service.Close()

	if err := service.Delete(); err != nil {
		return fmt.Errorf("delete service %s: %w", name, err)
	}
	return nil
}

func (windowsManager) StartService(name string) error {
	m, err := connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	service, _, err := openManaged(m, name)
	if err != nil {
		return err
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
	m, err := connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	service, _, err := openManaged(m, name)
	if err != nil {
		return err
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
	m, err := connect()
	if err != nil {
		return ServiceStatus{}, err
	}
	defer m.Disconnect()

	service, _, err := openManaged(m, name)
	if err != nil {
		return ServiceStatus{}, err
	}
	defer service.Close()

	return queryStatusOf(service, name)
}

func (windowsManager) QueryStatuses() ([]ServiceStatus, error) {
	m, err := connect()
	if err != nil {
		return nil, err
	}
	defer m.Disconnect()

	services, err := listManaged(m)
	if err != nil {
		return nil, err
	}

	statuses := make([]ServiceStatus, 0, len(services))
	for _, entry := range services {
		service, err := m.OpenService(entry.name)
		if err != nil {
			continue
		}
		status, err := queryStatusOf(service, entry.name)
		service.Close()
		if err != nil {
			continue
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (windowsManager) ListServices() ([]ServiceConfig, error) {
	m, err := connect()
	if err != nil {
		return nil, err
	}
	defer m.Disconnect()

	services, err := listManaged(m)
	if err != nil {
		return nil, err
	}

	configs := make([]ServiceConfig, 0, len(services))
	for _, entry := range services {
		configs = append(configs, serviceConfigFrom(entry.name, entry.config))
	}
	return configs, nil
}

func (windowsManager) GetService(name string) (ServiceConfig, error) {
	m, err := connect()
	if err != nil {
		return ServiceConfig{}, err
	}
	defer m.Disconnect()

	service, config, err := openManaged(m, name)
	if err != nil {
		return ServiceConfig{}, err
	}
	service.Close()
	return serviceConfigFrom(name, config), nil
}

func (windowsManager) GetServiceByHandle(handle ProcessHandle) (ServiceConfig, error) {
	process := windows.Handle(handle)
	pid, err := windows.GetProcessId(process)
	if err != nil {
		return ServiceConfig{}, fmt.Errorf("get process id: %w", err)
	}

	name, ok, err := serviceNameByPID(pid)
	if err != nil {
		return ServiceConfig{}, fmt.Errorf("resolve service by pid: %w", err)
	}
	if !ok {
		return ServiceConfig{}, ErrNotService
	}

	m, err := connect()
	if err != nil {
		return ServiceConfig{}, err
	}
	defer m.Disconnect()

	service, config, err := openManaged(m, name)
	if err != nil {
		return ServiceConfig{}, err
	}
	service.Close()

	if err := verifyAccount(process, config.ServiceStartName); err != nil {
		return ServiceConfig{}, err
	}
	if err := verifyImage(process, config.BinaryPathName); err != nil {
		return ServiceConfig{}, err
	}
	return serviceConfigFrom(name, config), nil
}

func connect() (*mgr.Mgr, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, fmt.Errorf("connect to SCM: %w", err)
	}
	return m, nil
}

// openManaged opens a service and verifies that it is an own-process Win32
// service created by vsystemd.
func openManaged(m *mgr.Mgr, name string) (*mgr.Service, mgr.Config, error) {
	service, err := m.OpenService(name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil, mgr.Config{}, ErrNotFound
		}
		return nil, mgr.Config{}, fmt.Errorf("open service %s: %w", name, err)
	}

	config, err := service.Config()
	if err != nil {
		service.Close()
		return nil, mgr.Config{}, fmt.Errorf("query config %s: %w", name, err)
	}
	if !isOwnProcessService(config) {
		service.Close()
		return nil, mgr.Config{}, fmt.Errorf("service %s is not an own-process Win32 service", name)
	}
	if config.LoadOrderGroup != managedServiceGroup {
		service.Close()
		return nil, mgr.Config{}, ErrNotManaged
	}
	return service, config, nil
}

func isOwnProcessService(config mgr.Config) bool {
	return config.ServiceType&windows.SERVICE_WIN32_OWN_PROCESS != 0 &&
		config.ServiceType&windows.SERVICE_WIN32_SHARE_PROCESS == 0
}

type managedService struct {
	name   string
	config mgr.Config
}

func listManaged(m *mgr.Mgr) ([]managedService, error) {
	names, err := m.ListServices()
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}

	services := make([]managedService, 0, len(names))
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
		services = append(services, managedService{name: name, config: config})
	}
	return services, nil
}

func serviceConfigFrom(name string, config mgr.Config) ServiceConfig {
	return ServiceConfig{
		Name:         name,
		BinaryPath:   config.BinaryPathName,
		StartType:    serviceStartType(config.StartType),
		Dependencies: config.Dependencies,
		User:         Account(config.ServiceStartName),
		DisplayName:  config.DisplayName,
		Description:  config.Description,
	}
}

func queryStatusOf(service *mgr.Service, name string) (ServiceStatus, error) {
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

func serviceNameByPID(pid uint32) (string, bool, error) {
	handle, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return "", false, err
	}
	defer windows.CloseServiceHandle(handle)

	var needed, returned, resume uint32
	size := uint32(8192)
	for {
		buf := make([]byte, size)
		err := windows.EnumServicesStatusEx(
			handle,
			windows.SC_ENUM_PROCESS_INFO,
			windows.SERVICE_WIN32,
			windows.SERVICE_ACTIVE,
			&buf[0],
			size,
			&needed,
			&returned,
			&resume,
			nil,
		)
		if err != nil {
			if errors.Is(err, windows.ERROR_MORE_DATA) {
				size = needed
				continue
			}
			return "", false, err
		}
		if returned == 0 {
			return "", false, nil
		}
		entries := unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), returned)
		for i := range entries {
			if entries[i].ServiceStatusProcess.ProcessId == pid {
				return windows.UTF16PtrToString(entries[i].ServiceName), true, nil
			}
		}
		return "", false, nil
	}
}

func verifyAccount(process windows.Handle, startName string) error {
	expected, err := accountSID(startName)
	if err != nil {
		return fmt.Errorf("resolve account %q: %w", startName, err)
	}

	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()

	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("token user: %w", err)
	}
	if !user.User.Sid.Equals(expected) {
		return errors.New("service account mismatch")
	}
	return nil
}

func verifyImage(process windows.Handle, binaryPathName string) error {
	binaryPath := firstCommandArg(binaryPathName)
	if binaryPath == "" {
		return errors.New("service has no binary path")
	}

	imagePath, err := processImagePath(process)
	if err != nil {
		return fmt.Errorf("process image: %w", err)
	}
	processFileID, err := fileIDOfPath(imagePath)
	if err != nil {
		return fmt.Errorf("process image file id: %w", err)
	}
	binaryFileID, err := fileIDOfPath(binaryPath)
	if err != nil {
		return fmt.Errorf("service binary file id: %w", err)
	}
	if processFileID != binaryFileID {
		return errors.New("service binary mismatch")
	}
	return nil
}

func processImagePath(process windows.Handle) (string, error) {
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(process, 0, &buf[0], &size); err != nil {
		buf = make([]uint16, 4096)
		size = uint32(len(buf))
		if retryErr := windows.QueryFullProcessImageName(process, 0, &buf[0], &size); retryErr != nil {
			return "", fmt.Errorf("query process image: %w", retryErr)
		}
	}
	return windows.UTF16ToString(buf[:size]), nil
}

func accountSID(name string) (*windows.SID, error) {
	account := strings.TrimSpace(name)
	if account == "" || strings.EqualFold(account, "LocalSystem") || strings.EqualFold(account, `NT AUTHORITY\SYSTEM`) {
		return windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	}

	accountPtr, err := windows.UTF16PtrFromString(account)
	if err != nil {
		return nil, err
	}
	sidBuf := make([]byte, maxSIDSize)
	sidLen := uint32(len(sidBuf))
	domainBuf := make([]uint16, 256)
	domainLen := uint32(len(domainBuf))
	var use uint32
	if err := windows.LookupAccountName(nil, accountPtr, (*windows.SID)(unsafe.Pointer(&sidBuf[0])), &sidLen, &domainBuf[0], &domainLen, &use); err != nil {
		return nil, err
	}
	return (*windows.SID)(unsafe.Pointer(&sidBuf[0])), nil
}

type fileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

func fileIDOfPath(path string) ([16]byte, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return [16]byte{}, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return [16]byte{}, err
	}
	defer windows.CloseHandle(handle)

	var info fileIDInfo
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileIdInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return [16]byte{}, err
	}
	return info.FileID, nil
}

func firstCommandArg(commandLine string) string {
	executable, _, err := splitBinaryPath(commandLine)
	if err != nil {
		return ""
	}
	return executable
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
	displayName := cfg.DisplayName
	if displayName == "" {
		displayName = cfg.Name
	}
	return mgr.Config{
		ServiceType:      windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:        windowsStartType(cfg.StartType),
		ErrorControl:     windows.SERVICE_ERROR_NORMAL,
		BinaryPathName:   cfg.BinaryPath,
		LoadOrderGroup:   managedServiceGroup,
		Dependencies:     cfg.Dependencies,
		ServiceStartName: string(cfg.User),
		DisplayName:      displayName,
		Password:         cfg.password,
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

func serviceStartType(t uint32) StartType {
	switch t {
	case windows.SERVICE_BOOT_START:
		return StartTypeBoot
	case windows.SERVICE_SYSTEM_START:
		return StartTypeSystem
	case windows.SERVICE_AUTO_START:
		return StartTypeAutomatic
	case windows.SERVICE_DISABLED:
		return StartTypeDisabled
	default:
		return StartTypeManual
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
