package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if cfg.SystemDir == "" || cfg.LogsDir == "" || cfg.TimersStateFile == "" {
		t.Errorf("paths are empty: %+v", cfg)
	}
	if cfg.Logs.MaxSizeMB != 10 || cfg.Logs.MaxAgeDays != 7 {
		t.Errorf("Logs = %+v", cfg.Logs)
	}
	if cfg.IPC.RequestTimeoutMS == 0 {
		t.Error("IPC defaults were not applied")
	}
	if cfg.Defaults.StartLimitBurst == 0 {
		t.Error("defaults were not applied")
	}
}

func TestLoadOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"system_dir":"C:\\custom\\system"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if cfg.SystemDir != `C:\custom\system` {
		t.Errorf("SystemDir = %q", cfg.SystemDir)
	}
	if cfg.LogsDir == "" {
		t.Error("LogsDir default was not applied")
	}
	if cfg.IPC.ConnectTimeoutMS == 0 {
		t.Error("IPC default was not applied")
	}
}

func TestLoadLogsCanBeDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"logs":{"max_size_mb":0,"max_age_days":0}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if cfg.Logs.MaxSizeMB != 0 || cfg.Logs.MaxAgeDays != 0 {
		t.Errorf("Logs = %+v", cfg.Logs)
	}
}

func TestLoadInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := LoadFrom(path); err == nil {
		t.Fatal("LoadFrom() error = nil")
	}
}

func TestInheritList(t *testing.T) {
	got := Environment{Inherit: " A , ,B "}.InheritList()
	if len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Errorf("InheritList() = %v", got)
	}
	if len((Environment{}).InheritList()) != 0 {
		t.Error("empty Inherit should yield no names")
	}
}

func TestLoadEnvironmentDefaults(t *testing.T) {
	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if len(cfg.Environment.InheritList()) == 0 {
		t.Error("default inherit list is empty")
	}
}

func TestLoadEnvironmentOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"environment":{"inherit":"A,B","vars":{"X":"%A%-y"}}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	got := cfg.Environment.InheritList()
	if len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Errorf("InheritList() = %v", got)
	}
	if cfg.Environment.Vars["X"] != "%A%-y" {
		t.Errorf("Vars = %v", cfg.Environment.Vars)
	}
}

func TestLoadEnvironmentEmptyInherit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"environment":{"inherit":""}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if len(cfg.Environment.InheritList()) != 0 {
		t.Errorf("InheritList() = %v, want empty", cfg.Environment.InheritList())
	}
}
