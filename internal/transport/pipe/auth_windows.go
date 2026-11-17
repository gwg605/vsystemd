//go:build windows

package pipe

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

// authenticator verifies the OS identity of an inbound connection and returns
// the trusted peer for it.
type authenticator interface {
	authenticate(handle windows.Handle, acceptTime time.Time) (transport.Peer, error)
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

	cfg, err := a.services.GetServiceByHandle(scm.ProcessHandle(client.proc))
	switch {
	case err == nil:
		name := unit.ID{Name: cfg.Name, Type: unit.TypeService}.String()
		return transport.NewPeer(transport.PeerRunner, name, false).WithService(cfg), nil
	case errors.Is(err, scm.ErrNotService):
		if !client.elevated {
			return transport.Peer{}, errors.New("control client is not elevated")
		}
		return transport.NewPeer(transport.PeerControl, fmt.Sprintf("systemctl_%d", client.pid), true), nil
	default:
		return transport.Peer{}, err
	}
}

type clientInfo struct {
	pid      uint32
	proc     windows.Handle
	token    windows.Token
	elevated bool
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

// resolveProcess opens a process and gathers its identity: elevation and
// creation time.
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
	if info.elevated, err = tokenElevated(info.token); err != nil {
		info.close()
		return nil, fmt.Errorf("token elevation: %w", err)
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
