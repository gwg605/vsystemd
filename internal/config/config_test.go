package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv(configEnv, filepath.Join(t.TempDir(), "missing.json"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
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
	t.Setenv(configEnv, path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
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
	t.Setenv(configEnv, path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
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
	t.Setenv(configEnv, path)

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil")
	}
}
