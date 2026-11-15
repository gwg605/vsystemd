package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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

const daemonServiceName = "vsystemd"

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
	console := parser.Flag("c", "console", &argparse.Options{
		Help: "run daemon in the foreground",
	})

	if err := parser.Parse(args); err != nil {
		return errors.New(parser.Usage(err))
	}

	switch *mode {
	case "daemon":
		return runDaemon(*console)
	case "run":
		if *unitName == "" {
			return errors.New(parser.Usage("--unit is required for run mode"))
		}
		return runUnit(*unitName)
	default:
		return errors.New(parser.Usage("unknown mode"))
	}
}

func runDaemon(console bool) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}

	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService && !console {
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
	slog.Info("vsystemd daemon starting", "console", console)

	store, err := storage.New(cfg.Paths())
	if err != nil {
		return err
	}

	service, err := daemon.New(scm.New(), store, executable, cfg.LogsDir, cfg.LogRotation(), transportFactory(cfg))
	if err != nil {
		return err
	}
	defer service.Close()

	if isService {
		err := daemon.RunService(daemonServiceName, service)
		if err != nil {
			slog.Error("vsystemd daemon exited with error", "error", err)
		} else {
			slog.Info("vsystemd daemon exited")
		}
		return err
	}
	return runDaemonConsole(service)
}

func runDaemonConsole(service *daemon.Daemon) error {
	if err := service.Start(); err != nil {
		return err
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	<-signals
	return nil
}

func runUnit(name string) error {
	if filepath.Ext(name) == "" {
		name += ".service"
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	startTime := time.Now().UTC()
	serviceName := strings.TrimSuffix(name, filepath.Ext(name))
	operationLog, err := logwriter.OpenManaged(
		filepath.Join(cfg.LogsDir, "vsystemd"),
		serviceName+"_",
		startTime,
		cfg.LogRotation(),
	)
	if err != nil {
		return err
	}
	defer operationLog.Close()
	logging.Configure(operationLog, cfg.LogLevel)
	slog.Info("vsystemd runner starting", "unit", name)

	store, err := storage.New(cfg.Paths())
	if err != nil {
		return err
	}
	data, err := store.ReadUnit(name)
	if err != nil {
		slog.Error("vsystemd runner: read unit failed", "unit", name, "error", err)
		return err
	}
	parsed, err := unit.Parse(name, data)
	if err != nil {
		slog.Error("vsystemd runner: parse unit failed", "unit", name, "error", err)
		return err
	}
	if parsed.Service == nil {
		return fmt.Errorf("%s is not a service unit", name)
	}
	for _, warning := range parsed.Warnings {
		slog.Warn("vsystemd runner: ignored unit directive", "unit", name, "warning", warning)
	}

	if err := logwriter.Cleanup(logwriter.ServiceLogDir(cfg.LogsDir, serviceName), cfg.Logs.MaxAgeDays, time.Now().UTC()); err != nil {
		slog.Debug("vsystemd runner: cleanup service logs failed", "unit", name, "error", err)
	}

	applyServiceDefaults(parsed.Service, cfg)
	logs, err := runner.NewLogStreamer(serviceName, parsed.Service, cfg.LogsDir, cfg.LogRotation(), startTime)
	if err != nil {
		slog.Error("vsystemd runner: create log streamer failed", "unit", name, "error", err)
		return err
	}
	defer logs.Close()

	envDefaults := runner.EnvironmentDefaults{
		Inherit: cfg.Environment.InheritList(),
		Vars:    cfg.Environment.Vars,
	}
	service, err := runner.NewRunner(name, parsed.Service, logs, envDefaults, transportFactory(cfg))
	if err != nil {
		return err
	}
	defer service.Close()

	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	var runErr error
	if isService {
		runErr = service.Run()
	} else {
		runErr = service.RunConsole()
	}
	if runErr != nil {
		slog.Error("vsystemd runner exited with error", "unit", name, "error", runErr)
	} else {
		slog.Info("vsystemd runner exited", "unit", name)
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

func transportFactory(cfg config.Config) func(transport.Peer, transport.MsgHandler) (transport.Transport, error) {
	return func(local transport.Peer, handler transport.MsgHandler) (transport.Transport, error) {
		return pipe.New(cfg.TransportConfig(), local, handler)
	}
}
