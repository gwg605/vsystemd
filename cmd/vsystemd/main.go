package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
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
	"github.com/gwg605/vsystemd/internal/unitpath"
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
	roots := unitpath.Roots{
		LogsDir:    cfg.LogsDir,
		StateDir:   cfg.StateDir,
		CacheDir:   cfg.CacheDir,
		ConfigDir:  cfg.ConfigDir,
		RuntimeDir: cfg.RunDir,
	}
	service, err := daemon.New(manager, store, executable, roots, cfg.LogRotation(), transportFactory(cfg, manager))
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
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	startTime := time.Now().UTC()
	operationLog, err := logwriter.OpenManaged(
		filepath.Join(cfg.LogsDir, "vsystemd"),
		strings.TrimSuffix(name, filepath.Ext(name))+"_",
		startTime,
		cfg.LogRotation(),
	)
	if err != nil {
		return err
	}
	defer operationLog.Close()
	logging.Configure(operationLog, cfg.LogLevel)
	slog.Info("vsystemd runner starting", "unit", name)

	store := func() (storage.Storage, error) {
		return storage.New(cfg.Paths())
	}
	service, err := runner.NewRunner(name, store, transportFactory(cfg, scm.New()), cfg, startTime)
	if err != nil {
		slog.Error("vsystemd runner: init failed", "unit", name, "error", err)
		return err
	}
	defer service.Close()

	runErr := service.Run()
	if runErr != nil {
		slog.Error("vsystemd runner exited with error", "unit", name, "error", runErr)
	} else {
		slog.Info("vsystemd runner exited", "unit", name)
	}
	return runErr
}

func transportFactory(cfg config.Config, services scm.Manager) transport.Factory {
	return func(local transport.Peer, handler transport.MsgHandler) (transport.Transport, error) {
		return pipe.New(cfg.TransportConfig(), local, handler, services)
	}
}
