package control

import (
	"testing"

	"github.com/gwg605/vsystemd/internal/scm"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	"github.com/gwg605/vsystemd/internal/storage"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
	"github.com/gwg605/vsystemd/internal/unit"
)

func newTestController(t *testing.T) (*Controller, *scmfake.Manager, *storagefake.Storage) {
	t.Helper()
	manager := scmfake.New()
	store := storagefake.New()
	ctrl := New(manager, nil, func() (storage.Storage, error) { return store, nil })
	return ctrl, manager, store
}

func TestEnableCreatesService(t *testing.T) {
	ctrl, manager, store := newTestController(t)
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\n"))

	id := unit.ID{Name: "app", Type: unit.TypeService}
	warnings, err := ctrl.Enable(id, `C:\vsystemd\vsystemd.exe`, "", "", false)
	if err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}

	cfg, err := manager.Config("app")
	if err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if cfg.BinaryPath != `"C:\vsystemd\vsystemd.exe" --mode=run --unit=app.service` {
		t.Errorf("BinaryPath = %q", cfg.BinaryPath)
	}
	if cfg.StartType != scm.StartTypeAutomatic {
		t.Errorf("StartType = %v", cfg.StartType)
	}
	if cfg.User != `NT SERVICE\app` {
		t.Errorf("User = %q", cfg.User)
	}
}

func TestEnableUpdatesExistingService(t *testing.T) {
	ctrl, manager, store := newTestController(t)
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\n"))
	if err := manager.CreateService(scm.ServiceConfig{Name: "app", BinaryPath: "old"}); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}

	id := unit.ID{Name: "app", Type: unit.TypeService}
	if _, err := ctrl.Enable(id, `C:\vsystemd\vsystemd.exe`, "", "", false); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}

	cfg, err := manager.Config("app")
	if err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if cfg.BinaryPath == "old" {
		t.Error("service config was not updated")
	}
}

func TestEnableRejectsManager(t *testing.T) {
	ctrl, _, _ := newTestController(t)
	id := unit.ID{Name: "vsystemd", Type: unit.TypeService}
	if _, err := ctrl.Enable(id, `C:\vsystemd\vsystemd.exe`, "", "", false); err == nil {
		t.Fatal("Enable() error = nil for the manager service")
	}
}

func TestDisableRemovesService(t *testing.T) {
	ctrl, manager, _ := newTestController(t)
	if err := manager.CreateService(scm.ServiceConfig{Name: "app"}); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}

	id := unit.ID{Name: "app", Type: unit.TypeService}
	if err := ctrl.Disable(id); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if _, err := manager.QueryStatus("app"); err == nil {
		t.Error("service still exists after Disable")
	}
}

func TestDisableMissingServiceIsNoop(t *testing.T) {
	ctrl, _, _ := newTestController(t)
	id := unit.ID{Name: "app", Type: unit.TypeService}
	if err := ctrl.Disable(id); err != nil {
		t.Errorf("Disable() error = %v", err)
	}
}

func TestStartStop(t *testing.T) {
	ctrl, manager, _ := newTestController(t)
	if err := manager.CreateService(scm.ServiceConfig{Name: "app"}); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}
	id := unit.ID{Name: "app", Type: unit.TypeService}

	if err := ctrl.Start(id); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	status, err := manager.QueryStatus("app")
	if err != nil {
		t.Fatalf("QueryStatus() error = %v", err)
	}
	if status.State != scm.StateRunning {
		t.Errorf("state = %v after Start", status.State)
	}

	if err := ctrl.Stop(id); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	status, err = manager.QueryStatus("app")
	if err != nil {
		t.Fatalf("QueryStatus() error = %v", err)
	}
	if status.State != scm.StateStopped {
		t.Errorf("state = %v after Stop", status.State)
	}
}
