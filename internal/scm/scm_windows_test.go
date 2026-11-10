//go:build windows

package scm

import (
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

func TestMapState(t *testing.T) {
	tests := []struct {
		state svc.State
		want  State
	}{
		{svc.Stopped, StateStopped},
		{svc.StartPending, StateStartPending},
		{svc.StopPending, StateStopPending},
		{svc.Running, StateRunning},
		{svc.ContinuePending, StateContinuePending},
		{svc.PausePending, StatePausePending},
		{svc.Paused, StatePaused},
	}
	for _, tt := range tests {
		if got := mapState(tt.state); got != tt.want {
			t.Errorf("mapState(%v) = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestWindowsStartType(t *testing.T) {
	tests := []struct {
		startType StartType
		want      uint32
	}{
		{StartTypeBoot, windows.SERVICE_BOOT_START},
		{StartTypeSystem, windows.SERVICE_SYSTEM_START},
		{StartTypeAutomatic, windows.SERVICE_AUTO_START},
		{StartTypeManual, windows.SERVICE_DEMAND_START},
		{StartTypeDisabled, windows.SERVICE_DISABLED},
	}
	for _, tt := range tests {
		if got := windowsStartType(tt.startType); got != tt.want {
			t.Errorf("windowsStartType(%v) = %d, want %d", tt.startType, got, tt.want)
		}
	}
}

func TestWindowsServiceConfig(t *testing.T) {
	cfg := ServiceConfig{
		Name:         "app",
		BinaryPath:   `C:\vsystemd\vsystemd.exe`,
		StartType:    StartTypeAutomatic,
		Dependencies: []string{"db"},
		User:         `NT SERVICE\app`,
		Password:     "secret",
		Description:  "Example",
	}

	got := windowsServiceConfig(cfg)
	if got.ServiceType != windows.SERVICE_WIN32_OWN_PROCESS {
		t.Errorf("ServiceType = %d", got.ServiceType)
	}
	if got.StartType != windows.SERVICE_AUTO_START {
		t.Errorf("StartType = %d", got.StartType)
	}
	if got.ErrorControl != windows.SERVICE_ERROR_NORMAL {
		t.Errorf("ErrorControl = %d", got.ErrorControl)
	}
	if got.BinaryPathName != cfg.BinaryPath {
		t.Errorf("BinaryPathName = %q", got.BinaryPathName)
	}
	if got.LoadOrderGroup != managedServiceGroup {
		t.Errorf("LoadOrderGroup = %q", got.LoadOrderGroup)
	}
	if !reflect.DeepEqual(got.Dependencies, cfg.Dependencies) {
		t.Errorf("Dependencies = %v", got.Dependencies)
	}
	if got.ServiceStartName != cfg.User || got.Password != cfg.Password {
		t.Errorf("account = %q/%q", got.ServiceStartName, got.Password)
	}
	if got.DisplayName != cfg.Name || got.Description != cfg.Description {
		t.Errorf("display = %q/%q", got.DisplayName, got.Description)
	}
}
