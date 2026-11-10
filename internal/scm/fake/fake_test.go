package fake

import (
	"testing"

	"github.com/gwg605/vsystemd/internal/scm"
)

func TestServiceLifecycle(t *testing.T) {
	m := New()
	cfg := scm.ServiceConfig{
		Name:       "app",
		BinaryPath: `C:\vsystemd\vsystemd.exe`,
		StartType:  scm.StartTypeAutomatic,
		User:       `NT SERVICE\app`,
	}

	if err := m.CreateService(cfg); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}
	if err := m.CreateService(cfg); err == nil {
		t.Error("CreateService() error = nil for duplicate service")
	}

	status, err := m.QueryStatus("app")
	if err != nil {
		t.Fatalf("QueryStatus() error = %v", err)
	}
	if status.State != scm.StateStopped {
		t.Errorf("State = %v", status.State)
	}

	if err := m.StartService("app"); err != nil {
		t.Fatalf("StartService() error = %v", err)
	}
	status, err = m.QueryStatus("app")
	if err != nil {
		t.Fatalf("QueryStatus() error = %v", err)
	}
	if status.State != scm.StateRunning || status.PID == 0 {
		t.Errorf("status = %+v after start", status)
	}

	if err := m.StopService("app"); err != nil {
		t.Fatalf("StopService() error = %v", err)
	}
	status, err = m.QueryStatus("app")
	if err != nil {
		t.Fatalf("QueryStatus() error = %v", err)
	}
	if status.State != scm.StateStopped || status.PID != 0 {
		t.Errorf("status = %+v after stop", status)
	}

	cfg.StartType = scm.StartTypeDisabled
	if err := m.UpdateService(cfg); err != nil {
		t.Fatalf("UpdateService() error = %v", err)
	}

	statuses, err := m.ListServices()
	if err != nil {
		t.Fatalf("ListServices() error = %v", err)
	}
	if len(statuses) != 1 || statuses[0].Name != "app" {
		t.Errorf("ListServices() = %v", statuses)
	}

	if err := m.SetStartType("app", scm.StartTypeManual); err != nil {
		t.Fatalf("SetStartType() error = %v", err)
	}
	config, err := m.Config("app")
	if err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if config.StartType != scm.StartTypeManual {
		t.Errorf("StartType = %v", config.StartType)
	}

	if err := m.DeleteService("app"); err != nil {
		t.Fatalf("DeleteService() error = %v", err)
	}
	if _, err := m.QueryStatus("app"); err == nil {
		t.Error("QueryStatus() error = nil after delete")
	}
}

func TestMissingService(t *testing.T) {
	m := New()
	if err := m.StartService("missing"); err == nil {
		t.Error("StartService() error = nil")
	}
	if err := m.StopService("missing"); err == nil {
		t.Error("StopService() error = nil")
	}
	if err := m.UpdateService(scm.ServiceConfig{Name: "missing"}); err == nil {
		t.Error("UpdateService() error = nil")
	}
	if err := m.SetStartType("missing", scm.StartTypeDisabled); err == nil {
		t.Error("SetStartType() error = nil")
	}
	if err := m.DeleteService("missing"); err == nil {
		t.Error("DeleteService() error = nil")
	}
}
