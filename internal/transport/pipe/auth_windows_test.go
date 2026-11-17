//go:build windows

package pipe

import (
	"testing"

	"github.com/gwg605/vsystemd/internal/transport"
)

func TestPipeServerVerifierControl(t *testing.T) {
	verifier := pipeServerVerifier{}
	tests := []struct {
		name      string
		peer      transport.Peer
		serverPID int
		wantErr   bool
	}{
		{"match", transport.NewPeer(transport.PeerControl, "systemctl_123", false), 123, false},
		{"mismatch", transport.NewPeer(transport.PeerControl, "systemctl_123", false), 456, true},
		{"invalid name", transport.NewPeer(transport.PeerControl, "systemctl_x", false), 1, true},
		{"unavailable pid", transport.NewPeer(transport.PeerControl, "systemctl_123", false), 0, true},
	}
	for _, test := range tests {
		if err := verifier.verify(test.peer, test.serverPID); (err != nil) != test.wantErr {
			t.Errorf("%s: verify() error = %v, wantErr %v", test.name, err, test.wantErr)
		}
	}
}
