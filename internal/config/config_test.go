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
	if filepath.Base(filepath.Dir(cfg.LogFile)) != "logs" {
		t.Errorf("LogFile = %q, want file under logs/", cfg.LogFile)
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
