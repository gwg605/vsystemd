package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/transport"
)

const defaultRoot = `C:\ProgramData\vsystemd`

// ManagerServiceName is the SCM name of the vsystemd manager service. It is
// managed like any other service but is excluded from unit listings and cannot
// be controlled through systemctl.
const ManagerServiceName = "vsystemd"

// defaultInheritEnv is the comma-separated list of environment variables
// inherited from the runner when building a clean service environment.
const defaultInheritEnv = "SystemRoot,SystemDrive,windir,PATH,PATHEXT,ComSpec,TEMP,TMP," +
	"NUMBER_OF_PROCESSORS,PROCESSOR_ARCHITECTURE,PROCESSOR_IDENTIFIER,OS," +
	"USERPROFILE,APPDATA,LOCALAPPDATA,HOMEDRIVE,HOMEPATH,ProgramData," +
	"ProgramFiles,ProgramFiles(x86),ProgramW6432,CommonProgramFiles,CommonProgramFiles(x86)," +
	"PUBLIC,ALLUSERSPROFILE,USERNAME,USERDOMAIN,USERDNSDOMAIN,COMPUTERNAME,LOGONSERVER,SESSIONNAME"

// Environment describes how the clean environment for service processes is
// built: Inherit lists variables taken from the runner's environment, Vars are
// extra variables whose values may reference the runner's environment via
// %NAME% placeholders.
type Environment struct {
	Inherit string            `json:"inherit"`
	Vars    map[string]string `json:"vars"`
}

// InheritList returns the inherited variable names.
func (e Environment) InheritList() []string {
	var names []string
	for _, name := range strings.Split(e.Inherit, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

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

type Logs struct {
	MaxSizeMB  int `json:"max_size_mb"`
	MaxAgeDays int `json:"max_age_days"`
}

type Config struct {
	SystemDir       string      `json:"system_dir"`
	LogsDir         string      `json:"logs_dir"`
	TimersStateFile string      `json:"timers_state_file"`
	LogLevel        string      `json:"log_level"`
	IPC             IPC         `json:"ipc"`
	Defaults        Defaults    `json:"defaults"`
	Logs            Logs        `json:"logs"`
	Environment     Environment `json:"environment"`
}

func Default() Config {
	return Config{
		SystemDir:       filepath.Join(defaultRoot, "system"),
		LogsDir:         filepath.Join(defaultRoot, "logs"),
		TimersStateFile: filepath.Join(defaultRoot, "timers.json"),
		LogLevel:        "info",
		IPC: IPC{
			ConnectTimeoutMS: 2000,
			IdleTimeoutMS:    5000,
			RequestTimeoutMS: 10000,
		},
		Defaults: Defaults{
			RestartSecMS:            100,
			TimeoutStopSecMS:        45000,
			StartLimitBurst:         5,
			StartLimitIntervalSecMS: 10000,
		},
		Logs: Logs{
			MaxSizeMB:  10,
			MaxAgeDays: 7,
		},
		Environment: Environment{
			Inherit: defaultInheritEnv,
		},
	}
}

func Load() (Config, error) {
	return LoadFrom(filepath.Join(defaultRoot, "config.json"))
}

// LoadFrom reads a config from an explicit path; a missing file yields defaults.
func LoadFrom(path string) (Config, error) {
	cfg := Default()
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

func (c Config) LogRotation() logwriter.Rotation {
	return logwriter.Rotation{
		MaxSizeMB:  c.Logs.MaxSizeMB,
		MaxAgeDays: c.Logs.MaxAgeDays,
	}
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
