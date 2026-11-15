//go:build windows

package pipe

import (
	"testing"

	"golang.org/x/sys/windows"
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
