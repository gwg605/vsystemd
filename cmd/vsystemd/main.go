package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/akamensky/argparse"
	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/daemon"
	"github.com/gwg605/vsystemd/internal/logging"
	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/runner"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/transport/pipe"
	"github.com/gwg605/vsystemd/internal/unit"
)

func main() {
	if err := run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	parser := argparse.NewParser("vsystemd", "Windows systemd implementation")
	mode := parser.Selector("m", "mode", []string{"daemon", "run"}, &argparse.Options{
		Required: true,
		Help:     "execution mode",
	})
	unitName := parser.String("u", "unit", &argparse.Options{
		Help: "unit name for run mode",
	})

	if err := parser.Parse(args); err != nil {
		return errors.New(parser.Usage(err))
	}

	switch *mode {
	case "daemon":
		return runDaemon()
	case "run":
		if *unitName == "" {
			return errors.New(parser.Usage("--unit is required for run mode"))
		}
		return runUnit(*unitName)
	default:
		return errors.New(parser.Usage("unknown mode"))
	}
}

func runDaemon() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}

	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		return startDaemonService(executable)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	startTime := time.Now().UTC()
	operationLog, err := logwriter.OpenManaged(
		filepath.Join(cfg.LogsDir, "vsystemd"),
		"daemon_",
		startTime,
		cfg.LogRotation(),
	)
	if err != nil {
		return err
	}
	defer operationLog.Close()
	logging.Configure(operationLog, cfg.LogLevel)
	slog.Info("vsystemd daemon starting")

	store, err := storage.New(cfg.Paths())
	if err != nil {
		return err
	}

	manager := scm.New()
	service, err := daemon.New(manager, store, executable, cfg.LogsDir, cfg.LogRotation(), transportFactory(cfg, manager))
	if err != nil {
		return err
	}
	defer service.Close()

	err = daemon.RunService(config.ManagerServiceName, service)
	if err != nil {
		slog.Error("vsystemd daemon exited with error", "error", err)
	} else {
		slog.Info("vsystemd daemon exited")
	}
	return err
}

func runUnit(name string) error {
	if filepath.Ext(name) == "" {
		name += ".service"
	}
	id, err := unit.ParseID(name)
	if err != nil {
		return err
	}
	if id.Type != unit.TypeService {
		return fmt.Errorf("%s is not a service unit", id)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	startTime := time.Now().UTC()
	operationLog, err := logwriter.OpenManaged(
		filepath.Join(cfg.LogsDir, "vsystemd"),
		id.Name+"_",
		startTime,
		cfg.LogRotation(),
	)
	if err != nil {
		return err
	}
	defer operationLog.Close()
	logging.Configure(operationLog, cfg.LogLevel)
	slog.Info("vsystemd runner starting", "unit", id.String())

	store, err := storage.New(cfg.Paths())
	if err != nil {
		return err
	}
	data, err := store.ReadUnit(id.String())
	if err != nil {
		slog.Error("vsystemd runner: read unit failed", "unit", id.String(), "error", err)
		return err
	}
	parsed, err := unit.Parse(id, data)
	if err != nil {
		slog.Error("vsystemd runner: parse unit failed", "unit", id.String(), "error", err)
		return err
	}
	if parsed.Service == nil {
		return fmt.Errorf("%s is not a service unit", id)
	}
	for _, warning := range parsed.Warnings {
		slog.Warn("vsystemd runner: ignored unit directive", "unit", id.String(), "warning", warning)
	}

	if err := logwriter.Cleanup(logwriter.ServiceLogDir(cfg.LogsDir, id.Name), cfg.Logs.MaxAgeDays, time.Now().UTC()); err != nil {
		slog.Debug("vsystemd runner: cleanup service logs failed", "unit", id.String(), "error", err)
	}

	applyServiceDefaults(parsed.Service, cfg)
	logs, err := runner.NewLogStreamer(id.Name, parsed.Service, cfg.LogsDir, cfg.LogRotation(), startTime)
	if err != nil {
		slog.Error("vsystemd runner: create log streamer failed", "unit", id.String(), "error", err)
		return err
	}
	defer logs.Close()

	envDefaults := runner.EnvironmentDefaults{
		Inherit: cfg.Environment.InheritList(),
		Vars:    cfg.Environment.Vars,
	}
	service, err := runner.NewRunner(id.String(), parsed.Service, logs, envDefaults, transportFactory(cfg, scm.New()))
	if err != nil {
		return err
	}
	defer service.Close()

	runErr := service.Run()
	if runErr != nil {
		slog.Error("vsystemd runner exited with error", "unit", id.String(), "error", runErr)
	} else {
		slog.Info("vsystemd runner exited", "unit", id.String())
	}
	return runErr
}

func applyServiceDefaults(service *unit.ServiceUnit, cfg config.Config) {
	if service.RestartSec == 0 {
		service.RestartSec = cfg.Defaults.RestartSec()
	}
	if service.TimeoutStopSec == 0 {
		service.TimeoutStopSec = cfg.Defaults.TimeoutStopSec()
	}
	if service.StartLimitBurst == 0 {
		service.StartLimitBurst = cfg.Defaults.StartLimitBurst
	}
	if service.StartLimitIntervalSec == 0 {
		service.StartLimitIntervalSec = cfg.Defaults.StartLimitIntervalSec()
	}
}

func transportFactory(cfg config.Config, services scm.Manager) func(transport.Peer, transport.MsgHandler) (transport.Transport, error) {
	return func(local transport.Peer, handler transport.MsgHandler) (transport.Transport, error) {
		return pipe.New(cfg.TransportConfig(), local, handler, services)
	}
}
