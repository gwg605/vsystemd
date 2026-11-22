//go:build windows

package scm

import (
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
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

func TestSplitBinaryPath(t *testing.T) {
	executable, args, err := splitBinaryPath(`"C:\vsystemd\vsystemd.exe" --mode=run --unit=app.service`)
	if err != nil {
		t.Fatalf("splitBinaryPath() error = %v", err)
	}
	if executable != `C:\vsystemd\vsystemd.exe` {
		t.Errorf("executable = %q", executable)
	}
	if !reflect.DeepEqual(args, []string{"--mode=run", "--unit=app.service"}) {
		t.Errorf("args = %v", args)
	}
}

func TestWindowsServiceConfig(t *testing.T) {
	cfg := ServiceConfig{
		Name:         "app",
		BinaryPath:   `C:\vsystemd\vsystemd.exe`,
		StartType:    StartTypeAutomatic,
		Dependencies: []string{"db"},
		User:         `NT SERVICE\app`,
		password:     "secret",
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
	if got.ServiceStartName != string(cfg.User) || got.Password != cfg.password {
		t.Errorf("account = %q/%q", got.ServiceStartName, got.Password)
	}
	if got.DisplayName != cfg.Name || got.Description != cfg.Description {
		t.Errorf("display = %q/%q", got.DisplayName, got.Description)
	}
}

func TestWindowsServiceConfigDisplayName(t *testing.T) {
	got := windowsServiceConfig(ServiceConfig{Name: "app", DisplayName: "App Service"})
	if got.DisplayName != "App Service" {
		t.Errorf("DisplayName = %q", got.DisplayName)
	}
}

func TestServiceStartType(t *testing.T) {
	tests := []struct {
		startType uint32
		want      StartType
	}{
		{windows.SERVICE_BOOT_START, StartTypeBoot},
		{windows.SERVICE_SYSTEM_START, StartTypeSystem},
		{windows.SERVICE_AUTO_START, StartTypeAutomatic},
		{windows.SERVICE_DEMAND_START, StartTypeManual},
		{windows.SERVICE_DISABLED, StartTypeDisabled},
	}
	for _, tt := range tests {
		if got := serviceStartType(tt.startType); got != tt.want {
			t.Errorf("serviceStartType(%d) = %v, want %v", tt.startType, got, tt.want)
		}
	}
}

func TestServiceConfigFrom(t *testing.T) {
	got := serviceConfigFrom("app", mgr.Config{
		BinaryPathName:   `"C:\vsystemd\vsystemd.exe" --mode=run --unit=app.service`,
		StartType:        windows.SERVICE_AUTO_START,
		Dependencies:     []string{"db"},
		ServiceStartName: `NT SERVICE\app`,
		DisplayName:      "App Service",
		Description:      "Example",
	})

	if got.Name != "app" || got.BinaryPath != `"C:\vsystemd\vsystemd.exe" --mode=run --unit=app.service` {
		t.Errorf("name/binary = %q/%q", got.Name, got.BinaryPath)
	}
	if got.StartType != StartTypeAutomatic {
		t.Errorf("StartType = %v", got.StartType)
	}
	if !reflect.DeepEqual(got.Dependencies, []string{"db"}) {
		t.Errorf("Dependencies = %v", got.Dependencies)
	}
	if got.User != `NT SERVICE\app` || got.DisplayName != "App Service" || got.Description != "Example" {
		t.Errorf("user/display/description = %q/%q/%q", got.User, got.DisplayName, got.Description)
	}
	if got.password != "" {
		t.Errorf("password = %q", got.password)
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
