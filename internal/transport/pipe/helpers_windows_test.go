//go:build windows

package pipe

import (
	"time"

	"golang.org/x/sys/windows"

	"github.com/gwg605/vsystemd/internal/transport"
)

// fixedAuth is a test authenticator that returns a pre-set peer, bypassing
// OS-level verification.
type fixedAuth struct {
	peer transport.Peer
}

func (a fixedAuth) authenticate(windows.Handle, time.Time) (transport.Peer, error) {
	return a.peer, nil
}
