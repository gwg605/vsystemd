//go:build windows

package pipe

import (
	"errors"
	"fmt"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/transport"
)

// authenticator verifies the OS identity of an inbound connection and returns
// the trusted peer for it.
type authenticator interface {
	authenticate(handle windows.Handle, acceptTime time.Time) (transport.Peer, error)
}

func authenticatorFor(local transport.Peer, services scm.Manager) authenticator {
	if local.Type() == transport.PeerDaemon {
		return clientAuthenticator{services: services}
	}
	return daemonOnlyAuthenticator{}
}

// clientAuthenticator verifies a client of the daemon pipe: either a managed
// service runner, or an elevated control client.
type clientAuthenticator struct {
	services scm.Manager
}

func (a clientAuthenticator) authenticate(handle windows.Handle, acceptTime time.Time) (transport.Peer, error) {
	client, err := resolveClient(handle, acceptTime)
	if err != nil {
		return transport.Peer{}, err
	}
	defer client.close()

	config, err := a.services.GetServiceByHandle(scm.ProcessHandle(client.proc))
	switch {
	case err == nil:
		return transport.NewPeer(transport.PeerRunner, config.Name+".service", false), nil
	case errors.Is(err, scm.ErrNotService):
		if !client.elevated {
			return transport.Peer{}, errors.New("control client is not elevated")
		}
		return transport.NewPeer(transport.PeerControl, fmt.Sprintf("systemctl_%d", client.pid), true), nil
	default:
		return transport.Peer{}, err
	}
}

// daemonOnlyAuthenticator accepts only the daemon, which runs with elevated
// rights (LocalSystem or an administrator).
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

type pipeServerVerifier struct {
	services scm.Manager
}

func (v pipeServerVerifier) verify(target transport.Peer, serverPID int) error {
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
		info, err := resolveProcess(pid)
		if err != nil {
			return err
		}
		defer info.close()
		config, err := v.services.GetServiceByHandle(scm.ProcessHandle(info.proc))
		if err != nil {
			return fmt.Errorf("validate runner service: %w", err)
		}
		if config.Name+".service" != target.Name() {
			return fmt.Errorf("runner pipe server is not service %s", target.Name())
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
	pid      uint32
	proc     windows.Handle
	token    windows.Token
	tokenSID *windows.SID
	elevated bool
	isSystem bool
	created  windows.Filetime
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

// resolveProcess opens a process and gathers its identity: token SID, elevation
// and system-ness.
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