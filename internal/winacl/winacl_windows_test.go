//go:build windows

package winacl

import (
	"os/user"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSIDLocalSystem(t *testing.T) {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid: %v", err)
	}
	for _, name := range []string{"", "LocalSystem", `NT AUTHORITY\SYSTEM`} {
		sid, err := SID(name)
		if err != nil {
			t.Fatalf("SID(%q): %v", name, err)
		}
		if !sid.Equals(system) {
			t.Errorf("SID(%q) = %v, want LocalSystem", name, sid)
		}
	}
}

func TestRulesForMode(t *testing.T) {
	owner, err := SID("LocalSystem")
	if err != nil {
		t.Fatalf("SID(LocalSystem): %v", err)
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid: %v", err)
	}

	rules := RulesForMode(0o750, owner, owner, everyone)
	if len(rules) != 2 {
		t.Fatalf("rules = %v", rules)
	}
	ownerRights := uint32(windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_EXECUTE | fileDeleteChild)
	if rules[0].Rights != ownerRights {
		t.Errorf("owner rights = %#x, want %#x", rules[0].Rights, ownerRights)
	}
	groupRights := uint32(windows.GENERIC_READ | windows.GENERIC_EXECUTE)
	if rules[1].Rights != groupRights {
		t.Errorf("group rights = %#x, want %#x", rules[1].Rights, groupRights)
	}
}

func TestSetDirectoryACL(t *testing.T) {
	dir := t.TempDir()
	me, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	owner, err := SID(me.Username)
	if err != nil {
		t.Fatalf("SID(%q): %v", me.Username, err)
	}
	if err := SetDirectoryACL(dir, 0o750, owner, nil); err != nil {
		t.Fatalf("SetDirectoryACL() error = %v", err)
	}

	desc, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo() error = %v", err)
	}
	acl, _, err := desc.DACL()
	if err != nil {
		t.Fatalf("DACL() error = %v", err)
	}
	if acl == nil {
		t.Error("DACL is nil")
	}
}
