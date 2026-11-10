package daemon

import (
	"reflect"
	"testing"

	"github.com/gwg605/vsystemd/internal/scm"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
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
		StartType:  scm.StartTypeAutomatic,
	}); err != nil {
		t.Fatalf("CreateService(removed) error = %v", err)
	}

	daemon := New(manager, store, `C:\vsystemd\vsystemd.exe`)
	if err := daemon.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
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
