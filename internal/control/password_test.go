package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePasswordProvided(t *testing.T) {
	password, err := resolvePassword("secret", "", false)
	if err != nil {
		t.Fatalf("resolvePassword() error = %v", err)
	}
	if password != "secret" {
		t.Errorf("password = %q", password)
	}

	password, err = resolvePassword("", "", false)
	if err != nil {
		t.Fatalf("resolvePassword() error = %v", err)
	}
	if password != "" {
		t.Errorf("password = %q", password)
	}
}

func TestResolvePasswordConflict(t *testing.T) {
	if _, err := resolvePassword("secret", "file", false); err == nil {
		t.Error("expected error for --password with --password-file")
	}
	if _, err := resolvePassword("", "file", true); err == nil {
		t.Error("expected error for --password-file with --password-stdin")
	}
}

func TestResolvePasswordFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pw.txt")
	if err := os.WriteFile(path, []byte("secret\r\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}
	password, err := resolvePassword("", path, false)
	if err != nil {
		t.Fatalf("resolvePassword() error = %v", err)
	}
	if password != "secret" {
		t.Errorf("password = %q", password)
	}
}

func TestReadPasswordStdin(t *testing.T) {
	password, err := readPassword(strings.NewReader("secret\n"))
	if err != nil {
		t.Fatalf("readPassword() error = %v", err)
	}
	if password != "secret" {
		t.Errorf("password = %q", password)
	}
}
