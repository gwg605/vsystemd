//go:build windows

package winacl

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// maxSIDSize is SECURITY_MAX_SID_SIZE.
	maxSIDSize = 68
	// fileDeleteChild is FILE_DELETE_CHILD, missing from x/sys.
	fileDeleteChild = 0x00000040
)

// Rule is one allow ACE to apply to a directory.
type Rule struct {
	SID    *windows.SID
	Rights uint32
}

// SID resolves an account or group name to its security identifier.
func SID(name string) (*windows.SID, error) {
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

// RulesForMode maps a Unix permission triplet (owner/group/other) to allow ACEs
// for the given principals. Zero-rights triplets are skipped.
func RulesForMode(mode os.FileMode, owner, group, other *windows.SID) []Rule {
	var rules []Rule
	if rights := modeRights((mode >> 6) & 0o7); rights != 0 && owner != nil {
		rules = append(rules, Rule{SID: owner, Rights: rights})
	}
	if rights := modeRights((mode >> 3) & 0o7); rights != 0 && group != nil {
		rules = append(rules, Rule{SID: group, Rights: rights})
	}
	if rights := modeRights(mode & 0o7); rights != 0 && other != nil {
		rules = append(rules, Rule{SID: other, Rights: rights})
	}
	return rules
}

func modeRights(mode os.FileMode) uint32 {
	var rights uint32
	if mode&0o4 != 0 {
		rights |= windows.GENERIC_READ
	}
	if mode&0o2 != 0 {
		rights |= windows.GENERIC_WRITE | fileDeleteChild
	}
	if mode&0o1 != 0 {
		rights |= windows.GENERIC_EXECUTE
	}
	return rights
}

// SetDirectoryACL replaces the directory's DACL (disabling inheritance) with
// the mode-derived ACEs plus SYSTEM and Administrators full control.
func SetDirectoryACL(path string, mode os.FileMode, owner, group *windows.SID) error {
	if group == nil {
		group = owner
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}

	rules := RulesForMode(mode, owner, group, everyone)
	rules = append(rules,
		Rule{SID: system, Rights: windows.GENERIC_ALL},
		Rule{SID: admins, Rights: windows.GENERIC_ALL},
	)
	return applyRules(path, rules)
}

func applyRules(path string, rules []Rule) error {
	var pinner runtime.Pinner
	defer pinner.Unpin()

	entries := make([]windows.EXPLICIT_ACCESS, 0, len(rules))
	for _, rule := range rules {
		pinner.Pin(rule.SID)
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.ACCESS_MASK(rule.Rights),
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.CONTAINER_INHERIT_ACE | windows.OBJECT_INHERIT_ACE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeValue: windows.TrusteeValueFromSID(rule.SID),
			},
		})
	}

	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	); err != nil {
		return fmt.Errorf("set DACL on %s: %w", path, err)
	}
	return nil
}
