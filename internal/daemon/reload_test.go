package daemon

import (
	"reflect"
	"testing"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/scm"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
	"github.com/gwg605/vsystemd/internal/transport"
	transportfake "github.com/gwg605/vsystemd/internal/transport/fake"
	"github.com/gwg605/vsystemd/internal/unit"
)

func TestReload(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte(`[Unit]
Description=App
Requires=db.service
After=network.target

[Service]
ExecStart=C:\app.exe
`))
	store.WriteUnit("new.service", []byte(`[Service]
ExecStart=C:\new.exe
`))
	store.WriteUnit("job.timer", []byte(`[Timer]
OnCalendar=daily
`))

	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{
		Name:       "app",
		BinaryPath: "old",
		StartType:  scm.StartTypeDisabled,
		User:       "old",
	}); err != nil {
		t.Fatalf("CreateService(app) error = %v", err)
	}
	if err := manager.CreateService(scm.ServiceConfig{
		Name:       "removed",
		BinaryPath: "old",
		StartType:  scm.StartTypeDisabled,
	}); err != nil {
		t.Fatalf("CreateService(removed) error = %v", err)
	}

	network := transportfake.NewNetwork()
	daemon, err := New(manager, store, `C:\vsystemd\vsystemd.exe`, t.TempDir(), logwriter.Rotation{}, func(local transport.Peer, handler transport.MsgHandler) (transport.Transport, error) {
		return network.New(transport.DefaultConfig(), local, handler), nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer daemon.Close()
	if err := daemon.reload(); err != nil {
		t.Fatalf("reload() error = %v", err)
	}

	config, err := manager.Config("app")
	if err != nil {
		t.Fatalf("Config(app) error = %v", err)
	}
	if config.BinaryPath != `"C:\vsystemd\vsystemd.exe" --mode=run --unit=app.service` {
		t.Errorf("BinaryPath = %q", config.BinaryPath)
	}
	if config.Description != "App" {
		t.Errorf("Description = %q", config.Description)
	}
	if !reflect.DeepEqual(config.Dependencies, []string{"db"}) {
		t.Errorf("Dependencies = %v", config.Dependencies)
	}
	if config.User != `NT SERVICE\app` {
		t.Errorf("User = %q", config.User)
	}
	if config.StartType != scm.StartTypeDisabled {
		t.Errorf("StartType = %v", config.StartType)
	}

	if _, err := manager.QueryStatus("removed"); err == nil {
		t.Error("removed service still exists")
	}
	if _, err := manager.QueryStatus("new"); err == nil {
		t.Error("new service was created")
	}
}

func TestReloadKeepsManagerService(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\n"))

	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "app"}); err != nil {
		t.Fatalf("CreateService(app) error = %v", err)
	}
	if err := manager.CreateService(scm.ServiceConfig{Name: config.ManagerServiceName}); err != nil {
		t.Fatalf("CreateService(manager) error = %v", err)
	}

	daemon, _ := newTestDaemon(t, manager, store)
	if err := daemon.reload(); err != nil {
		t.Fatalf("reload() error = %v", err)
	}
	if _, err := manager.QueryStatus(config.ManagerServiceName); err != nil {
		t.Errorf("manager service was removed by reload: %v", err)
	}
}

func TestReloadKeepsMissingUnitService(t *testing.T) {
	store := storagefake.New()
	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "orphan", StartType: scm.StartTypeAutomatic}); err != nil {
		t.Fatalf("CreateService(orphan) error = %v", err)
	}

	daemon, _ := newTestDaemon(t, manager, store)
	if err := daemon.reload(); err != nil {
		t.Fatalf("reload() error = %v", err)
	}

	if _, err := manager.QueryStatus("orphan"); err != nil {
		t.Errorf("orphan was removed: %v", err)
	}
	if entry, _ := daemon.units.entry(unit.ID{Name: "orphan", Type: unit.TypeService}); entry.loadState != LoadStateNotFound {
		t.Errorf("orphan LoadState = %q", entry.loadState)
	}
}

func TestReloadDeletesDisabledMissingUnit(t *testing.T) {
	store := storagefake.New()
	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "orphan", StartType: scm.StartTypeDisabled}); err != nil {
		t.Fatalf("CreateService(orphan) error = %v", err)
	}

	daemon, _ := newTestDaemon(t, manager, store)
	if err := daemon.reload(); err != nil {
		t.Fatalf("reload() error = %v", err)
	}

	if _, err := manager.QueryStatus("orphan"); err == nil {
		t.Error("orphan still exists")
	}
	if _, ok := daemon.State("orphan.service"); ok {
		t.Error("orphan state was not cleared")
	}
}
