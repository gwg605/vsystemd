package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/akamensky/argparse"
)

type unitCommand struct {
	name string
	cmd  *argparse.Command
	unit *string
}

func main() {
	if err := run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	parser := argparse.NewParser("systemctl", "Control vsystemd units")

	var unitCommands []unitCommand
	for _, name := range []string{"enable", "disable", "start", "stop", "restart", "status"} {
		cmd := parser.NewCommand(name, name+" unit")
		unit := cmd.StringPositional(&argparse.Options{Help: "unit name"})
		unitCommands = append(unitCommands, unitCommand{name: name, cmd: cmd, unit: unit})
	}

	listUnits := parser.NewCommand("list-units", "List known units")
	daemonReload := parser.NewCommand("daemon-reload", "Reload unit files")

	if err := parser.Parse(args); err != nil {
		return errors.New(parser.Usage(err))
	}

	for _, command := range unitCommands {
		if command.cmd.Happened() {
			if *command.unit == "" {
				return errors.New(parser.Usage("unit name is required"))
			}
			return dispatch(command.name, *command.unit)
		}
	}

	switch {
	case listUnits.Happened():
		return dispatch("list-units", "")
	case daemonReload.Happened():
		return dispatch("daemon-reload", "")
	default:
		return errors.New(parser.Usage("command is required"))
	}
}

func dispatch(command, unit string) error {
	if unit == "" {
		return fmt.Errorf("%s is not implemented yet", command)
	}
	return fmt.Errorf("%s is not implemented yet: %s", command, unit)
}
