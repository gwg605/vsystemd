package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/akamensky/argparse"

	vsystemd "github.com/gwg605/vsystemd"
	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/control"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/transport/pipe"
	"github.com/gwg605/vsystemd/internal/unit"
	"github.com/gwg605/vsystemd/internal/unitpath"
)

type unitCommand struct {
	name string
	cmd  *argparse.Command
	unit *string
}

func main() {
	configureLogging("none")
	if err := run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// configureLogging enables diagnostic logging to stdout or stderr. The default
// "none" discards all logs.
func configureLogging(target string) {
	var out io.Writer
	switch target {
	case "stdout":
		out = os.Stdout
	case "stderr":
		out = os.Stderr
	default:
		out = io.Discard
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

func run(args []string) error {
	parser := argparse.NewParser("systemctl", "Control vsystemd units")

	logTarget := parser.Selector("", "log", []string{"none", "stdout", "stderr"}, &argparse.Options{
		Default: "none",
		Help:    "enable diagnostic logging",
	})

	enableCmd := parser.NewCommand("enable", "Enable unit")
	enableUnit := enableCmd.StringPositional(&argparse.Options{Help: "unit name"})
	password := enableCmd.String("p", "password", &argparse.Options{Help: "password for the service account"})
	passwordFile := enableCmd.String("", "password-file", &argparse.Options{Help: "read the service account password from a file"})
	passwordStdin := enableCmd.Flag("", "password-stdin", &argparse.Options{Help: "read the service account password from stdin"})

	var unitCommands []unitCommand
	for _, name := range []string{"disable", "start", "stop", "restart", "status"} {
		cmd := parser.NewCommand(name, name+" unit")
		unitName := cmd.StringPositional(&argparse.Options{Help: "unit name"})
		unitCommands = append(unitCommands, unitCommand{name: name, cmd: cmd, unit: unitName})
	}

	listUnits := parser.NewCommand("list-units", "List known units")
	listTimers := parser.NewCommand("list-timers", "List timers")
	listTimersAll := listTimers.Flag("a", "all", &argparse.Options{Help: "show stopped and disabled timers too"})
	daemonReload := parser.NewCommand("daemon-reload", "Reload unit files")
	versionCmd := parser.NewCommand("--version", "Print version")

	if err := parser.Parse(args); err != nil {
		return errors.New(parser.Usage(err))
	}

	configureLogging(*logTarget)

	if versionCmd.Happened() {
		fmt.Printf("systemctl %s\n%s\n", vsystemd.Version, vsystemd.FeatureFlags)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctrl := newController(cfg)

	if enableCmd.Happened() {
		if *enableUnit == "" {
			return errors.New(parser.Usage("unit name is required"))
		}
		id, err := parseUnit(*enableUnit)
		if err != nil {
			return err
		}
		if id.Type == unit.TypeTimer {
			return ctrl.TimerEnable(id, true)
		}
		return enableService(ctrl, id, *password, *passwordFile, *passwordStdin)
	}

	for _, command := range unitCommands {
		if command.cmd.Happened() {
			if *command.unit == "" {
				return errors.New(parser.Usage("unit name is required"))
			}
			id, err := parseUnit(*command.unit)
			if err != nil {
				return err
			}
			return dispatch(ctrl, cfg, command.name, id)
		}
	}

	switch {
	case listUnits.Happened():
		list, err := ctrl.ListUnits()
		if err != nil {
			return err
		}
		printUnits(list)
		return nil
	case listTimers.Happened():
		list, err := ctrl.ListUnits()
		if err != nil {
			return err
		}
		printTimers(list, *listTimersAll)
		return nil
	case daemonReload.Happened():
		return ctrl.DaemonReload()
	default:
		return errors.New(parser.Usage("command is required"))
	}
}

func newController(cfg config.Config) *control.Controller {
	transport := func(local transport.Peer, handler transport.MsgHandler) (transport.Transport, error) {
		return pipe.New(cfg.TransportConfig(), local, handler, nil)
	}
	store := func() (storage.Storage, error) {
		return storage.New(cfg.Paths())
	}
	roots := unitpath.Roots{
		LogsDir:    cfg.LogsDir,
		StateDir:   cfg.StateDir,
		CacheDir:   cfg.CacheDir,
		ConfigDir:  cfg.ConfigDir,
		RuntimeDir: cfg.RunDir,
	}
	return control.New(scm.New(), transport, store, roots)
}

func parseUnit(name string) (unit.ID, error) {
	if filepath.Ext(name) == "" {
		name += unit.TypeService.Extension()
	}
	return unit.ParseID(name)
}

func enableService(ctrl *control.Controller, id unit.ID, password, passwordFile string, passwordStdin bool) error {
	executable, err := control.ExecutablePath()
	if err != nil {
		return err
	}
	warnings, err := ctrl.Enable(id, executable, password, passwordFile, passwordStdin)
	for _, warning := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s: %s\n", id, warning)
	}
	return err
}

func dispatch(ctrl *control.Controller, cfg config.Config, command string, id unit.ID) error {
	if id.Type == unit.TypeTimer {
		return dispatchTimer(ctrl, cfg, command, id)
	}

	switch command {
	case "disable":
		return ctrl.Disable(id)
	case "start":
		return ctrl.Start(id)
	case "stop":
		return ctrl.Stop(id)
	case "restart":
		return ctrl.Restart(id)
	case "status":
		status, err := ctrl.Status(id)
		if err != nil {
			return err
		}
		printServiceStatus(cfg, id, status)
		return nil
	default:
		return fmt.Errorf("unknown command %s", command)
	}
}

func dispatchTimer(ctrl *control.Controller, cfg config.Config, command string, id unit.ID) error {
	switch command {
	case "enable":
		return ctrl.TimerEnable(id, true)
	case "disable":
		return ctrl.TimerEnable(id, false)
	case "start":
		return ctrl.TimerStart(id)
	case "stop":
		return ctrl.TimerStop(id)
	case "status":
		status, err := ctrl.Status(id)
		if err != nil {
			return err
		}
		printTimerStatus(cfg, id, status)
		return nil
	default:
		return fmt.Errorf("command %s is not supported for timer units", command)
	}
}
