package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/unit"
)

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

func TestTimerActive(t *testing.T) {
	running := true
	stopped := false
	tests := []struct {
		name  string
		entry protocol.UnitStatus
		want  bool
	}{
		{"in scheduler", protocol.UnitStatus{Running: &running}, true},
		{"stopped", protocol.UnitStatus{Running: &stopped}, false},
		{"not scheduled", protocol.UnitStatus{}, false},
	}
	for _, test := range tests {
		if got := timerActive(test.entry); got != test.want {
			t.Errorf("%s: timerActive() = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestTimerStatusLines(t *testing.T) {
	enabled := true
	activeSince := time.Now().Add(-2 * time.Hour).UTC()
	nextRun := time.Now().Add(15 * time.Hour).UTC()
	status := protocol.StatusResponse{
		Unit:         "job.timer",
		SystemdState: "active (waiting)",
		Description:  "Daily backup",
		Enabled:      &enabled,
		ActiveSince:  &activeSince,
		NextRun:      &nextRun,
		Activates:    "backup.service",
		LogTail:      []string{"backup-line"},
	}
	id := unit.ID{Name: "job", Type: unit.TypeTimer}

	lines := timerStatusLines(config.Config{SystemDir: `C:\units`}, id, status)
	if len(lines) != 7 {
		t.Fatalf("lines = %v", lines)
	}
	if lines[0] != "● job.timer - Daily backup" {
		t.Errorf("header = %q", lines[0])
	}
	if want := `     Loaded: loaded (C:\units\job.timer; enabled)`; lines[1] != want {
		t.Errorf("loaded = %q, want %q", lines[1], want)
	}
	if !strings.HasPrefix(lines[2], "     Active: active (waiting) since ") || !strings.HasSuffix(lines[2], "ago") {
		t.Errorf("active = %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "    Trigger: ") || !strings.HasSuffix(lines[3], "left") {
		t.Errorf("trigger = %q", lines[3])
	}
	if lines[4] != "   Triggers: ● backup.service" {
		t.Errorf("triggers = %q", lines[4])
	}
	if lines[5] != "" {
		t.Errorf("separator = %q", lines[5])
	}
	if lines[6] != "backup-line" {
		t.Errorf("log tail = %q", lines[6])
	}
}

func TestServiceStatusLines(t *testing.T) {
	enabled := true
	lastTransition := time.Now().Add(-40 * time.Minute).UTC()
	status := protocol.StatusResponse{
		Unit:           "app.service",
		SystemdState:   "active (running)",
		Description:    "App",
		Enabled:        &enabled,
		LastTransition: lastTransition,
		PID:            777,
		ExitCode:       1,
		RestartCount:   2,
		LastError:      "boom",
		LogTail:        []string{"log-line"},
	}
	id := unit.ID{Name: "app", Type: unit.TypeService}

	lines := serviceStatusLines(config.Config{SystemDir: `C:\units`}, id, status)
	if len(lines) != 9 {
		t.Fatalf("lines = %v", lines)
	}
	if lines[0] != "● app.service - App" {
		t.Errorf("header = %q", lines[0])
	}
	if want := `     Loaded: loaded (C:\units\app.service; enabled)`; lines[1] != want {
		t.Errorf("loaded = %q, want %q", lines[1], want)
	}
	if !strings.HasPrefix(lines[2], "     Active: active (running) since ") || !strings.HasSuffix(lines[2], "ago") {
		t.Errorf("active = %q", lines[2])
	}
	if lines[3] != "   Main PID: 777" {
		t.Errorf("main pid = %q", lines[3])
	}
	if lines[4] != "  Exit code: 1" {
		t.Errorf("exit code = %q", lines[4])
	}
	if lines[5] != "    Restart: 2" {
		t.Errorf("restart = %q", lines[5])
	}
	if lines[6] != "      Error: boom" {
		t.Errorf("error = %q", lines[6])
	}
	if lines[7] != "" {
		t.Errorf("separator = %q", lines[7])
	}
	if lines[8] != "log-line" {
		t.Errorf("log tail = %q", lines[8])
	}
}
