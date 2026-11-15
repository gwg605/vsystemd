//go:build windows

package pipe

import (
	"testing"

	"golang.org/x/sys/windows"

	"github.com/gwg605/vsystemd/internal/transport"
)

func TestAccountSIDKnownAccounts(t *testing.T) {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid: %v", err)
	}
	for _, name := range []string{"", "LocalSystem", `NT AUTHORITY\SYSTEM`} {
		sid, err := accountSID(name)
		if err != nil {
			t.Fatalf("accountSID(%q): %v", name, err)
		}
		if !sid.Equals(system) {
			t.Errorf("accountSID(%q) = %v, want LocalSystem", name, sid)
		}
	}
}

func TestFirstCommandArg(t *testing.T) {
	tests := map[string]string{
		`"C:\path\vsystemd.exe" --mode=run --unit=app.service`: `C:\path\vsystemd.exe`,
		`C:\path\vsystemd.exe`:                                 `C:\path\vsystemd.exe`,
		``:                                                     ``,
	}
	for input, want := range tests {
		if got := firstCommandArg(input); got != want {
			t.Errorf("firstCommandArg(%q) = %q, want %q", input, got, want)
		}
	}
}

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
