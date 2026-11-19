package scm

import (
	"testing"

	"github.com/gwg605/vsystemd/internal/unit"
)

func TestNewAccount(t *testing.T) {
	if got := NewAccount(`CONTOSO\alice`, "app"); got != `CONTOSO\alice` {
		t.Errorf("NewAccount() = %q", got)
	}
	if got := NewAccount("", "app"); got != `NT SERVICE\app` {
		t.Errorf("NewAccount() = %q", got)
	}
}

func TestNewServiceConfig(t *testing.T) {
	file := &unit.UnitFile{
		Name:        unit.ID{Name: "app", Type: unit.TypeService},
		Description: "App",
		Requires:    []string{"db.service"},
		Service:     &unit.ServiceUnit{},
	}

	cfg := NewServiceConfig(file, `C:\vsystemd\vsystemd.exe`, StartTypeAutomatic)

	if cfg.Name != "app" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if cfg.BinaryPath != `"C:\vsystemd\vsystemd.exe" --mode=run --unit=app.service` {
		t.Errorf("BinaryPath = %q", cfg.BinaryPath)
	}
	if cfg.StartType != StartTypeAutomatic {
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
	if cfg.password != "" {
		t.Errorf("password = %q", cfg.password)
	}
}
