//go:build windows

package pipe

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/gwg605/vsystemd/internal/transport"
)

const (
	// maxSIDSize is SECURITY_MAX_SID_SIZE.
	maxSIDSize = 68
	// daemonServiceName is the SCM name of the daemon service.
	daemonServiceName = "vsystemd"
)

// authenticator verifies the OS identity of an inbound connection and returns
// the trusted peer for it.
type authenticator interface {
	authenticate(handle windows.Handle, acceptTime time.Time) (transport.Peer, error)
}

func authenticatorFor(local transport.Peer) authenticator {
	if local.Type() == transport.PeerDaemon {
		return clientAuthenticator{}
	}
	return daemonOnlyAuthenticator{}
}

// clientAuthenticator verifies a client of the daemon pipe: either a service
// runner matching its SCM parameters, or an elevated control client.
type clientAuthenticator struct{}

func (clientAuthenticator) authenticate(handle windows.Handle, acceptTime time.Time) (transport.Peer, error) {
	client, err := resolveClient(handle, acceptTime)
	if err != nil {
		return transport.Peer{}, err
	}
	defer client.close()

	name, isService, err := serviceNameByPID(client.pid)
	if err != nil {
		return transport.Peer{}, fmt.Errorf("resolve service by pid: %w", err)
	}
	if isService {
		return client.verifyRunner(name)
	}
	if !client.elevated {
		return transport.Peer{}, errors.New("control client is not elevated")
	}
	return transport.NewPeer(transport.PeerControl, fmt.Sprintf("systemctl_%d", client.pid), true), nil
}

func (c *clientInfo) verifyRunner(serviceName string) (transport.Peer, error) {
	cfg, err := serviceConfig(serviceName)
	if err != nil {
		return transport.Peer{}, fmt.Errorf("service %s config: %w", serviceName, err)
	}
	if cfg.ServiceType&windows.SERVICE_WIN32_OWN_PROCESS == 0 || cfg.ServiceType&windows.SERVICE_WIN32_SHARE_PROCESS != 0 {
		return transport.Peer{}, fmt.Errorf("service %s is not an own-process service", serviceName)
	}

	expected, err := accountSID(cfg.ServiceStartName)
	if err != nil {
		return transport.Peer{}, fmt.Errorf("resolve account %q: %w", cfg.ServiceStartName, err)
	}
	if !c.tokenSID.Equals(expected) {
		return transport.Peer{}, fmt.Errorf("service %s account mismatch", serviceName)
	}

	binaryPath := firstCommandArg(cfg.BinaryPathName)
	if binaryPath == "" {
		return transport.Peer{}, fmt.Errorf("service %s has no binary path", serviceName)
	}
	processFileID, err := fileIDOfPath(c.imagePath)
	if err != nil {
		return transport.Peer{}, fmt.Errorf("process image file id: %w", err)
	}
	binaryFileID, err := fileIDOfPath(binaryPath)
	if err != nil {
		return transport.Peer{}, fmt.Errorf("service binary file id: %w", err)
	}
	if processFileID != binaryFileID {
		return transport.Peer{}, fmt.Errorf("service %s binary mismatch", serviceName)
	}
	return transport.NewPeer(transport.PeerRunner, serviceName+".service", false), nil
}

// daemonOnlyAuthenticator accepts only the daemon, which runs as LocalSystem
// (service) or as an elevated administrator (foreground/console mode).
type daemonOnlyAuthenticator struct{}

func (daemonOnlyAuthenticator) authenticate(handle windows.Handle, acceptTime time.Time) (transport.Peer, error) {
	client, err := resolveClient(handle, acceptTime)
	if err != nil {
		return transport.Peer{}, err
	}
	defer client.close()
	if !client.isSystem && !client.elevated {
		return transport.Peer{}, errors.New("inbound peer is not the daemon")
	}
	return transport.NewPeer(transport.PeerDaemon, transport.PeerDaemonName, false), nil
}

// serverVerifier verifies the process on the server end of an outbound pipe so
// that a squatter on the pipe name cannot impersonate a peer.
type serverVerifier interface {
	verify(target transport.Peer, serverPID int) error
}

type pipeServerVerifier struct{}

func (pipeServerVerifier) verify(target transport.Peer, serverPID int) error {
	if serverPID <= 0 {
		return errors.New("server pid is unavailable")
	}
	pid := uint32(serverPID)
	switch target.Type() {
	case transport.PeerControl:
		expected, ok := controlPID(target.Name())
		if !ok {
			return fmt.Errorf("invalid control peer %q", target.Name())
		}
		if expected != pid {
			return fmt.Errorf("control pipe server pid %d, want %d", pid, expected)
		}
	case transport.PeerRunner:
		name, isService, err := serviceNameByPID(pid)
		if err != nil {
			return fmt.Errorf("resolve server service: %w", err)
		}
		if !isService || name+".service" != target.Name() {
			return fmt.Errorf("runner pipe server is not service %s", target.Name())
		}
		info, err := resolveProcess(pid)
		if err != nil {
			return err
		}
		defer info.close()
		if _, err := info.verifyRunner(name); err != nil {
			return err
		}
	case transport.PeerDaemon:
		info, err := resolveProcess(pid)
		if err != nil {
			return fmt.Errorf("resolve daemon server: %w", err)
		}
		defer info.close()
		if !info.isSystem && !info.elevated {
			return errors.New("daemon pipe server is not the daemon")
		}
	default:
		return fmt.Errorf("unsupported target %s", target)
	}
	return nil
}

// controlPID extracts the process id encoded in a control peer name
// ("systemctl_<pid>").
func controlPID(name string) (uint32, bool) {
	pid, err := strconv.ParseUint(controlSuffix(name), 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(pid), true
}

type clientInfo struct {
	pid       uint32
	proc      windows.Handle
	token     windows.Token
	tokenSID  *windows.SID
	imagePath string
	elevated  bool
	isSystem  bool
	created   windows.Filetime
}

func (c *clientInfo) close() {
	if c.token != 0 {
		c.token.Close()
	}
	if c.proc != 0 {
		windows.CloseHandle(c.proc)
	}
}

func resolveClient(handle windows.Handle, acceptTime time.Time) (*clientInfo, error) {
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(handle, &pid); err != nil {
		return nil, fmt.Errorf("client pid: %w", err)
	}
	info, err := resolveProcess(pid)
	if err != nil {
		return nil, err
	}
	if info.created.Nanoseconds() >= acceptTime.UnixNano() {
		info.close()
		return nil, errors.New("process created after the connection (possible pid reuse)")
	}
	return info, nil
}

// resolveProcess opens a process and gathers its identity: image path, token
// SID, elevation and system-ness.
func resolveProcess(pid uint32) (*clientInfo, error) {
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return nil, fmt.Errorf("open process %d: %w", pid, err)
	}
	info := &clientInfo{pid: pid, proc: proc}

	var exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(proc, &info.created, &exit, &kernel, &user); err != nil {
		info.close()
		return nil, fmt.Errorf("process times: %w", err)
	}

	if info.imagePath, err = processImagePath(proc); err != nil {
		info.close()
		return nil, err
	}

	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &info.token); err != nil {
		info.close()
		return nil, fmt.Errorf("open process token: %w", err)
	}
	tokenUser, err := info.token.GetTokenUser()
	if err != nil {
		info.close()
		return nil, fmt.Errorf("token user: %w", err)
	}
	info.tokenSID = tokenUser.User.Sid

	if info.elevated, err = tokenElevated(info.token); err != nil {
		info.close()
		return nil, fmt.Errorf("token elevation: %w", err)
	}
	if systemSID, sidErr := windows.CreateWellKnownSid(windows.WinLocalSystemSid); sidErr == nil {
		info.isSystem = info.tokenSID.Equals(systemSID)
	}
	return info, nil
}

func tokenElevated(token windows.Token) (bool, error) {
	var elevation uint32
	var returned uint32
	err := windows.GetTokenInformation(
		token,
		windows.TokenElevation,
		(*byte)(unsafe.Pointer(&elevation)),
		uint32(unsafe.Sizeof(elevation)),
		&returned,
	)
	if err != nil {
		return false, err
	}
	return elevation != 0, nil
}

func processImagePath(proc windows.Handle) (string, error) {
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(proc, 0, &buf[0], &size); err != nil {
		buf = make([]uint16, 4096)
		size = uint32(len(buf))
		if retryErr := windows.QueryFullProcessImageName(proc, 0, &buf[0], &size); retryErr != nil {
			return "", fmt.Errorf("query process image: %w", retryErr)
		}
	}
	return windows.UTF16ToString(buf[:size]), nil
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

func serviceConfig(name string) (mgr.Config, error) {
	manager, err := mgr.Connect()
	if err != nil {
		return mgr.Config{}, err
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(name)
	if err != nil {
		return mgr.Config{}, err
	}
	defer service.Close()

	return service.Config()
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
	parts, err := windows.DecomposeCommandLine(commandLine)
	if err != nil || len(parts) == 0 {
		return ""
	}
	return parts[0]
}
