package svcconfig

import (
	"testing"

	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/unit"
)

func TestFromUnit(t *testing.T) {
	file := &unit.UnitFile{
		Name:        unit.ID{Name: "app", Type: unit.TypeService},
		Description: "App",
		DisplayName: "App Service",
		Requires:    []string{"db.service"},
		Service:     &unit.ServiceUnit{},
	}

	cfg := FromUnit(file, `C:\vsystemd\vsystemd.exe`, scm.StartTypeAutomatic, nil)

	if cfg.Name != "app" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if cfg.BinaryPath != `"C:\vsystemd\vsystemd.exe" --mode=run --unit=app.service` {
		t.Errorf("BinaryPath = %q", cfg.BinaryPath)
	}
	if cfg.StartType != scm.StartTypeAutomatic {
		t.Errorf("StartType = %v", cfg.StartType)
	}
	if len(cfg.Dependencies) != 1 || cfg.Dependencies[0] != "db" {
		t.Errorf("Dependencies = %v", cfg.Dependencies)
	}
	if cfg.User != `NT SERVICE\app` {
		t.Errorf("User = %q", cfg.User)
	}
	if cfg.Description != "App" {
		t.Errorf("Description = %q", cfg.Description)
	}
	if cfg.DisplayName != "App Service" {
		t.Errorf("DisplayName = %q", cfg.DisplayName)
	}
}
