package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/akamensky/argparse"
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
	unit := parser.String("u", "unit", &argparse.Options{
		Help: "unit name for run mode",
	})

	if err := parser.Parse(args); err != nil {
		return errors.New(parser.Usage(err))
	}

	switch *mode {
	case "daemon":
		return runDaemon()
	case "run":
		if *unit == "" {
			return errors.New(parser.Usage("--unit is required for run mode"))
		}
		return runUnit(*unit)
	default:
		return errors.New(parser.Usage("unknown mode"))
	}
}

func runDaemon() error {
	return errors.New("daemon mode is not implemented yet")
}

func runUnit(name string) error {
	return fmt.Errorf("run mode is not implemented yet: %s", name)
}