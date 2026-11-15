//go:build windows

package pipe

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// conn is the minimal inbound stream interface. serverConn also exposes the
// underlying pipe handle so a later phase can authenticate the client.
type conn interface {
	io.Reader
	io.Writer
	io.Closer
}

type serverConn struct {
	handle windows.Handle
	io.ReadWriteCloser
}

func (c *serverConn) Handle() windows.Handle { return c.handle }

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
		if _, waitErr := windows.WaitForSingleObject(event, windows.INFINITE); waitErr != nil {
			l.clearConnecting(h)
			windows.CloseHandle(h)
			return nil, waitErr
		}
		var done uint32
		if resultErr := windows.GetOverlappedResult(h, overlapped, &done, false); resultErr != nil {
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
		slog.Warn("pipe: connect failed", "path", l.path, "error", err)
		return nil, err
	}

	l.clearConnecting(h)
	slog.Debug("pipe: connected", "path", l.path, "handle", uintptr(h))

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
		slog.Debug("pipe: pre-create instance failed", "path", l.path, "error", nextErr)
	}

	file, err := winio.NewOpenFile(h)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return &serverConn{handle: h, ReadWriteCloser: file}, nil
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
