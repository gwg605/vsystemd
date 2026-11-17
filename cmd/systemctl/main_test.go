package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gwg605/vsystemd/internal/unit"
)

func TestPasswordRequired(t *testing.T) {
	tests := map[string]bool{
		`NT SERVICE\app`:              false,
		`nt service\app`:              false,
		`LocalSystem`:                 false,
		`SYSTEM`:                      false,
		`NT AUTHORITY\SYSTEM`:         false,
		`NT AUTHORITY\LocalService`:   false,
		`NT AUTHORITY\NetworkService`: false,
		`LOCAL SERVICE`:               false,
		`NETWORK SERVICE`:             false,
		`CONTOSO\alice`:               true,
		`.\alice`:                     true,
	}
	for account, want := range tests {
		if got := passwordRequired(account); got != want {
			t.Errorf("passwordRequired(%q) = %v, want %v", account, got, want)
		}
	}
}

func TestResolvePasswordProvided(t *testing.T) {
	password, err := resolvePassword(`CONTOSO\alice`, "secret", "", false)
	if err != nil {
		t.Fatalf("resolvePassword() error = %v", err)
	}
	if password != "secret" {
		t.Errorf("password = %q", password)
	}

	password, err = resolvePassword("LocalSystem", "", "", false)
	if err != nil {
		t.Fatalf("resolvePassword() error = %v", err)
	}
	if password != "" {
		t.Errorf("password = %q", password)
	}
}

func TestResolvePasswordConflict(t *testing.T) {
	if _, err := resolvePassword(`CONTOSO\alice`, "secret", "file", false); err == nil {
		t.Error("expected error for --password with --password-file")
	}
	if _, err := resolvePassword(`CONTOSO\alice`, "", "file", true); err == nil {
		t.Error("expected error for --password-file with --password-stdin")
	}
}

func TestResolvePasswordFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pw.txt")
	if err := os.WriteFile(path, []byte("secret\r\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}
	password, err := resolvePassword(`CONTOSO\alice`, "", path, false)
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

func TestParseUnit(t *testing.T) {
	id, err := parseUnit("app")
	if err != nil {
		t.Fatalf("parseUnit(app) error = %v", err)
	}
	if id != (unit.ID{Name: "app", Type: unit.TypeService}) {
		t.Errorf("parseUnit(app) = %+v", id)
	}

	id, err = parseUnit("job.timer")
	if err != nil {
		t.Fatalf("parseUnit(job.timer) error = %v", err)
	}
	if id != (unit.ID{Name: "job", Type: unit.TypeTimer}) {
		t.Errorf("parseUnit(job.timer) = %+v", id)
	}

	for _, bad := range []string{"../evil", "bad name", "app.txt"} {
		if _, err := parseUnit(bad); err == nil {
			t.Errorf("parseUnit(%q) error = nil", bad)
		}
	}
}

func TestUnitPathWarnings(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "app.log")
	envFile := filepath.Join(dir, "app.env")
	if err := os.WriteFile(envFile, []byte("A=B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missingEnv := filepath.Join(dir, "missing.env")
	dotdot := dir + string(filepath.Separator) + ".." + string(filepath.Separator) + "app.log"

	tests := []struct {
		name    string
		service *unit.ServiceUnit
		want    int
	}{
		{"valid", &unit.ServiceUnit{StandardOutput: "file:" + good, EnvironmentFiles: []string{envFile}}, 0},
		{"optional missing env", &unit.ServiceUnit{EnvironmentFiles: []string{"-" + missingEnv}}, 0},
		{"relative output", &unit.ServiceUnit{StandardOutput: "file:relative.log"}, 1},
		{"dotdot output", &unit.ServiceUnit{StandardOutput: "file:" + dotdot}, 1},
		{"missing output dir", &unit.ServiceUnit{StandardOutput: "file:" + filepath.Join(dir, "missing", "app.log")}, 1},
		{"unsupported output", &unit.ServiceUnit{StandardOutput: "journal"}, 1},
		{"relative env", &unit.ServiceUnit{EnvironmentFiles: []string{"relative.env"}}, 1},
		{"missing env", &unit.ServiceUnit{EnvironmentFiles: []string{missingEnv}}, 1},
	}
	for _, test := range tests {
		warnings := unitPathWarnings(&unit.UnitFile{Service: test.service})
		if len(warnings) != test.want {
			t.Errorf("%s: warnings = %v, want %d", test.name, warnings, test.want)
		}
	}
}
