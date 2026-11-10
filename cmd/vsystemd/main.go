package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/akamensky/argparse"
	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/daemon"
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
	store, err := storage.New(cfg.Paths())
	if err != nil {
		return err
	}

	service, err := daemon.New(scm.New(), store, executable, transportFactory(cfg))
	if err != nil {
		return err
	}
	defer service.Close()

	if isService {
		return daemon.RunService(daemonServiceName, service)
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
	store, err := storage.New(cfg.Paths())
	if err != nil {
		return err
	}
	data, err := store.ReadUnit(name)
	if err != nil {
		return err
	}
	parsed, err := unit.Parse(name, data)
	if err != nil {
		return err
	}
	if parsed.Service == nil {
		return fmt.Errorf("%s is not a service unit", name)
	}

	applyServiceDefaults(parsed.Service, cfg)
	service, err := runner.NewRunner(name, parsed.Service, store, transportFactory(cfg))
	if err != nil {
		return err
	}
	defer service.Close()

	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if isService {
		return service.Run()
	}
	return service.RunConsole()
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

func transportFactory(cfg config.Config) func(string, transport.MsgHandler) (transport.Transport, error) {
	return func(peerName string, handler transport.MsgHandler) (transport.Transport, error) {
		return pipe.New(cfg.TransportConfig(), peerName, handler)
	}
}
