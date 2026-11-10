package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/transport"
)

const (
	defaultRoot = `C:\ProgramData\vsystemd`
	configEnv   = "VSYSTEMD_CONFIG"
)

type IPC struct {
	ConnectTimeoutMS int `json:"connect_timeout_ms"`
	IdleTimeoutMS    int `json:"idle_timeout_ms"`
	RequestTimeoutMS int `json:"request_timeout_ms"`
}

type Defaults struct {
	RestartSecMS            int `json:"restart_sec_ms"`
	TimeoutStopSecMS        int `json:"timeout_stop_sec_ms"`
	StartLimitBurst         int `json:"start_limit_burst"`
	StartLimitIntervalSecMS int `json:"start_limit_interval_sec_ms"`
}

type Config struct {
	SystemDir       string   `json:"system_dir"`
	LogsDir         string   `json:"logs_dir"`
	TimersStateFile string   `json:"timers_state_file"`
	LogFile         string   `json:"log_file"`
	LogLevel        string   `json:"log_level"`
	IPC             IPC      `json:"ipc"`
	Defaults        Defaults `json:"defaults"`
}

func Default() Config {
	return Config{
		SystemDir:       filepath.Join(defaultRoot, "system"),
		LogsDir:         filepath.Join(defaultRoot, "logs"),
		TimersStateFile: filepath.Join(defaultRoot, "timers.json"),
		LogFile:         filepath.Join(defaultRoot, "vsystemd.log"),
		LogLevel:        "info",
		IPC: IPC{
			ConnectTimeoutMS: 2000,
			IdleTimeoutMS:    5000,
			RequestTimeoutMS: 10000,
		},
		Defaults: Defaults{
			RestartSecMS:            100,
			TimeoutStopSecMS:        90000,
			StartLimitBurst:         5,
			StartLimitIntervalSecMS: 10000,
		},
	}
}

func Load() (Config, error) {
	cfg := Default()
	path := os.Getenv(configEnv)
	if path == "" {
		path = filepath.Join(defaultRoot, "config.json")
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c Config) Paths() storage.Paths {
	return storage.Paths{
		SystemDir:  c.SystemDir,
		LogsDir:    c.LogsDir,
		TimersFile: c.TimersStateFile,
	}
}

func (c Config) TransportConfig() transport.Config {
	return transport.Config{
		ConnectTimeout: time.Duration(c.IPC.ConnectTimeoutMS) * time.Millisecond,
		IdleTimeout:    time.Duration(c.IPC.IdleTimeoutMS) * time.Millisecond,
		RequestTimeout: time.Duration(c.IPC.RequestTimeoutMS) * time.Millisecond,
	}
}

func (d Defaults) RestartSec() time.Duration {
	return time.Duration(d.RestartSecMS) * time.Millisecond
}

func (d Defaults) TimeoutStopSec() time.Duration {
	return time.Duration(d.TimeoutStopSecMS) * time.Millisecond
}

func (d Defaults) StartLimitIntervalSec() time.Duration {
	return time.Duration(d.StartLimitIntervalSecMS) * time.Millisecond
}

func (c *Config) applyDefaults() {
	defaults := Default()
	if c.SystemDir == "" {
		c.SystemDir = defaults.SystemDir
	}
	if c.LogsDir == "" {
		c.LogsDir = defaults.LogsDir
	}
	if c.TimersStateFile == "" {
		c.TimersStateFile = defaults.TimersStateFile
	}
	if c.LogFile == "" {
		c.LogFile = defaults.LogFile
	}
	if c.LogLevel == "" {
		c.LogLevel = defaults.LogLevel
	}
	if c.IPC.ConnectTimeoutMS <= 0 {
		c.IPC.ConnectTimeoutMS = defaults.IPC.ConnectTimeoutMS
	}
	if c.IPC.IdleTimeoutMS <= 0 {
		c.IPC.IdleTimeoutMS = defaults.IPC.IdleTimeoutMS
	}
	if c.IPC.RequestTimeoutMS <= 0 {
		c.IPC.RequestTimeoutMS = defaults.IPC.RequestTimeoutMS
	}
	if c.Defaults.RestartSecMS <= 0 {
		c.Defaults.RestartSecMS = defaults.Defaults.RestartSecMS
	}
	if c.Defaults.TimeoutStopSecMS <= 0 {
		c.Defaults.TimeoutStopSecMS = defaults.Defaults.TimeoutStopSecMS
	}
	if c.Defaults.StartLimitBurst <= 0 {
		c.Defaults.StartLimitBurst = defaults.Defaults.StartLimitBurst
	}
	if c.Defaults.StartLimitIntervalSecMS <= 0 {
		c.Defaults.StartLimitIntervalSecMS = defaults.Defaults.StartLimitIntervalSecMS
	}
}
