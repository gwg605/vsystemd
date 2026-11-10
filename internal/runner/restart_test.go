package runner

import (
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/unit"
)

func TestRestartPolicy(t *testing.T) {
	tests := []struct {
		policy   unit.RestartPolicy
		exitCode int
		want     bool
	}{
		{unit.RestartNo, 0, false},
		{unit.RestartNo, 1, false},
		{unit.RestartAlways, 0, true},
		{unit.RestartAlways, 1, true},
		{unit.RestartOnFailure, 0, false},
		{unit.RestartOnFailure, 1, true},
	}
	for _, tt := range tests {
		controller := NewRestartController(&unit.ServiceUnit{Restart: tt.policy})
		if got := controller.ShouldRestart(tt.exitCode); got != tt.want {
			t.Errorf("Restart=%s exit=%d: ShouldRestart() = %v, want %v", tt.policy, tt.exitCode, got, tt.want)
		}
	}
}

func TestRestartDelay(t *testing.T) {
	controller := NewRestartController(&unit.ServiceUnit{RestartSec: 5 * time.Second})
	if got := controller.Delay(); got != 5*time.Second {
		t.Errorf("Delay() = %v", got)
	}
}

func TestRestartCount(t *testing.T) {
	controller := NewRestartController(&unit.ServiceUnit{})
	controller.RecordRestart()
	controller.RecordRestart()
	if got := controller.RestartCount(); got != 2 {
		t.Errorf("RestartCount() = %d", got)
	}

	controller.Reset()
	if got := controller.RestartCount(); got != 0 {
		t.Errorf("RestartCount() = %d after reset", got)
	}
}

func TestStartLimit(t *testing.T) {
	controller := NewRestartController(&unit.ServiceUnit{
		StartLimitBurst:       2,
		StartLimitIntervalSec: time.Minute,
	})
	now := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	controller.now = func() time.Time { return now }

	if !controller.AllowStart() {
		t.Fatal("first AllowStart() = false")
	}
	if !controller.AllowStart() {
		t.Fatal("second AllowStart() = false")
	}
	if controller.AllowStart() {
		t.Fatal("third AllowStart() = true")
	}

	now = now.Add(time.Minute + time.Second)
	if !controller.AllowStart() {
		t.Error("AllowStart() = false after interval elapsed")
	}

	controller.Reset()
	if !controller.AllowStart() {
		t.Error("AllowStart() = false after reset")
	}
}
