package main

import (
	"os"
	"path/filepath"
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
	password, err := resolvePassword(`CONTOSO\alice`, "secret")
	if err != nil {
		t.Fatalf("resolvePassword() error = %v", err)
	}
	if password != "secret" {
		t.Errorf("password = %q", password)
	}

	password, err = resolvePassword("LocalSystem", "")
	if err != nil {
		t.Fatalf("resolvePassword() error = %v", err)
	}
	if password != "" {
		t.Errorf("password = %q", password)
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
