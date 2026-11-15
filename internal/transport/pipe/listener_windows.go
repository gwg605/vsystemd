//go:build windows

package pipe

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// errnoPipeClosing is ERROR_PIPE_CLOSING.
const errnoPipeClosing = syscall.Errno(230)

// conn is the inbound/outbound pipe stream. It exposes the pipe handle and a
// "[client->server]" pid tag for logging.
type conn interface {
	io.Reader
	io.Writer
	io.Closer
	Handle() windows.Handle
	tag() string
}

type pipeConn struct {
	handle    windows.Handle
	clientPID int
	serverPID int
	io.ReadWriteCloser
}

func newPipeConn(handle windows.Handle, rwc io.ReadWriteCloser) *pipeConn {
	c := &pipeConn{handle: handle, ReadWriteCloser: rwc}
	var pid uint32
	if windows.GetNamedPipeClientProcessId(handle, &pid) == nil {
		c.clientPID = int(pid)
	}
	if windows.GetNamedPipeServerProcessId(handle, &pid) == nil {
		c.serverPID = int(pid)
	}
	return c
}

func (c *pipeConn) Handle() windows.Handle { return c.handle }

func (c *pipeConn) tag() string { return fmt.Sprintf("[%d->%d]", c.clientPID, c.serverPID) }

// pipeListener is a small Windows named-pipe server that exposes the handle of
// every accepted connection (go-winio's listener hides it).
type pipeListener struct {
	path string
	sddl string

	mu         sync.Mutex
	pending    windows.Handle
	connecting windows.Handle
	closed     bool
	first      bool
}

func listenPipe(path, sddl string) (*pipeListener, error) {
	l := &pipeListener{path: path, sddl: sddl, first: true}
	h, err := l.newInstance()
	if err != nil {
		return nil, err
	}
	l.pending = h
	slog.Debug("pipe: listener created", "path", path)
	return l, nil
}

func (l *pipeListener) newInstance() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(l.path)
	if err != nil {
		return 0, err
	}
	// SecurityDescriptorFromString returns a Go-heap copy; do not LocalFree it.
	sd, err := windows.SecurityDescriptorFromString(l.sddl)
	if err != nil {
		return 0, err
	}

	sa := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}

	flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if l.first {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
		l.first = false
	}
	return windows.CreateNamedPipe(
		name,
		flags,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		windows.PIPE_UNLIMITED_INSTANCES,
		0, 0, 0,
		&sa,
	)
}

func (l *pipeListener) Accept() (conn, error) {
	for {
		c, err := l.acceptOne()
		if err == nil {
			return c, nil
		}
		if errors.Is(err, net.ErrClosed) {
			return nil, err
		}
		if isTransientConnectError(err) {
			slog.Debug("pipe: retrying accept after transient connect error", "path", l.path, "error", err)
			continue
		}
		return nil, err
	}
}

// isTransientConnectError reports connect errors caused by a client that
// connected and disconnected before the server completed ConnectNamedPipe.
func isTransientConnectError(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case errnoPipeClosing, windows.ERROR_NO_DATA, windows.ERROR_IO_INCOMPLETE:
		return true
	default:
		return false
	}
}

func (l *pipeListener) acceptOne() (conn, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, net.ErrClosed
	}
	h := l.pending
	l.pending = 0
	l.mu.Unlock()

	if h == 0 {
		var err error
		if h, err = l.newInstance(); err != nil {
			return nil, err
		}
	}

	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	defer windows.CloseHandle(event)

	var overlapped = new(windows.Overlapped)
	overlapped.HEvent = event

	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		windows.CloseHandle(h)
		return nil, net.ErrClosed
	}
	l.connecting = h
	l.mu.Unlock()

	err = windows.ConnectNamedPipe(h, overlapped)
	switch {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		// already connected
	case errors.Is(err, windows.ERROR_IO_PENDING):
		l.mu.Lock()
		closed := l.closed
		l.mu.Unlock()
		if closed {
			_ = windows.CancelIoEx(h, overlapped)
		}
		var done uint32
		if resultErr := windows.GetOverlappedResult(h, overlapped, &done, true); resultErr != nil {
			l.clearConnecting(h)
			windows.CloseHandle(h)
			if errors.Is(resultErr, windows.ERROR_OPERATION_ABORTED) {
				return nil, net.ErrClosed
			}
			return nil, resultErr
		}
	default:
		l.clearConnecting(h)
		windows.CloseHandle(h)
		l.mu.Lock()
		closed := l.closed
		l.mu.Unlock()
		if closed {
			return nil, net.ErrClosed
		}
		return nil, err
	}

	l.clearConnecting(h)

	// Reserve the next instance before serving this one so the pipe name stays
	// available for subsequent clients.
	if next, nextErr := l.newInstance(); nextErr == nil {
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			windows.CloseHandle(next)
		} else {
			l.pending = next
			l.mu.Unlock()
		}
	} else {
		slog.Warn("pipe: pre-create instance failed", "path", l.path, "error", nextErr)
	}

	file, err := winio.NewOpenFile(h)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	c := newPipeConn(h, file)
	slog.Debug(pipeMsg(c.tag(), "connected"), "path", l.path)
	return c, nil
}

func (l *pipeListener) clearConnecting(h windows.Handle) {
	l.mu.Lock()
	if l.connecting == h {
		l.connecting = 0
	}
	l.mu.Unlock()
}

func (l *pipeListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	pending := l.pending
	l.pending = 0
	connecting := l.connecting
	l.mu.Unlock()

	if pending != 0 {
		windows.CloseHandle(pending)
	}
	if connecting != 0 {
		_ = windows.CancelIoEx(connecting, nil)
	}
	slog.Debug("pipe: listener closed", "path", l.path)
	return nil
}
