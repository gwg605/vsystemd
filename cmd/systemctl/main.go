package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akamensky/argparse"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/transport/pipe"
	"github.com/gwg605/vsystemd/internal/unit"
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

	enableCmd := parser.NewCommand("enable", "Enable unit")
	enableUnit := enableCmd.StringPositional(&argparse.Options{Help: "unit name"})
	password := enableCmd.String("p", "password", &argparse.Options{Help: "password for the service account"})

	var unitCommands []unitCommand
	for _, name := range []string{"disable", "start", "stop", "restart", "status"} {
		cmd := parser.NewCommand(name, name+" unit")
		unitName := cmd.StringPositional(&argparse.Options{Help: "unit name"})
		unitCommands = append(unitCommands, unitCommand{name: name, cmd: cmd, unit: unitName})
	}

	listUnits := parser.NewCommand("list-units", "List known units")
	daemonReload := parser.NewCommand("daemon-reload", "Reload unit files")

	if err := parser.Parse(args); err != nil {
		return errors.New(parser.Usage(err))
	}

	if enableCmd.Happened() {
		if *enableUnit == "" {
			return errors.New(parser.Usage("unit name is required"))
		}
		name := normalizeUnit(*enableUnit)
		if strings.HasSuffix(name, ".timer") {
			return timerCommand(protocol.TypeTimerEnable, protocol.TimerRequest{Unit: name, Enabled: true})
		}
		return enableService(name, *password)
	}

	for _, command := range unitCommands {
		if command.cmd.Happened() {
			if *command.unit == "" {
				return errors.New(parser.Usage("unit name is required"))
			}
			return dispatch(command.name, normalizeUnit(*command.unit))
		}
	}

	switch {
	case listUnits.Happened():
		return listUnitsCommand()
	case daemonReload.Happened():
		return daemonReloadCommand()
	default:
		return errors.New(parser.Usage("command is required"))
	}
}

func normalizeUnit(name string) string {
	if filepath.Ext(name) == "" {
		return name + ".service"
	}
	return name
}

func dispatch(command, name string) error {
	if strings.HasSuffix(name, ".timer") {
		return dispatchTimer(command, name)
	}
	if !strings.HasSuffix(name, ".service") {
		return fmt.Errorf("unsupported unit type")
	}

	manager := scm.New()
	serviceName := strings.TrimSuffix(name, ".service")

	switch command {
	case "disable":
		return manager.SetStartType(serviceName, scm.StartTypeDisabled)
	case "start":
		return manager.StartService(serviceName)
	case "stop":
		return manager.StopService(serviceName)
	case "restart":
		if err := manager.StopService(serviceName); err != nil {
			return err
		}
		if err := waitStopped(manager, serviceName, 2*time.Minute); err != nil {
			return err
		}
		return manager.StartService(serviceName)
	case "status":
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		return statusCommand(cfg, name)
	default:
		return fmt.Errorf("unknown command %s", command)
	}
}

func dispatchTimer(command, name string) error {
	switch command {
	case "enable":
		return timerCommand(protocol.TypeTimerEnable, protocol.TimerRequest{Unit: name, Enabled: true})
	case "disable":
		return timerCommand(protocol.TypeTimerEnable, protocol.TimerRequest{Unit: name, Enabled: false})
	case "start":
		return timerCommand(protocol.TypeTimerStart, protocol.TimerRequest{Unit: name})
	case "stop":
		return timerCommand(protocol.TypeTimerStop, protocol.TimerRequest{Unit: name})
	default:
		return fmt.Errorf("command %s is not supported for timer units", command)
	}
}

func timerCommand(msgType string, request protocol.TimerRequest) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	response, err := daemonRequest(cfg, msgType, request)
	if err != nil {
		return err
	}
	if err := responseError(response); err != nil {
		return err
	}

	var result protocol.TimerResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("%s failed", request.Unit)
	}
	return nil
}

func enableService(name, password string) error {
	if !strings.HasSuffix(name, ".service") {
		return fmt.Errorf("%s is not a service unit", name)
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

	executable, err := vsystemdPath()
	if err != nil {
		return err
	}
	serviceName := strings.TrimSuffix(name, ".service")
	serviceConfig := scm.ServiceConfig{
		Name:         serviceName,
		BinaryPath:   `"` + executable + `" --mode=run --unit=` + name,
		StartType:    scm.StartTypeAutomatic,
		Dependencies: unitDependencies(parsed),
		User:         unitUser(parsed.Service.User, serviceName),
		Password:     password,
		Description:  parsed.Description,
	}

	manager := scm.New()
	if _, err := manager.QueryStatus(serviceName); err != nil {
		return manager.CreateService(serviceConfig)
	}
	return manager.UpdateService(serviceConfig)
}

func statusCommand(cfg config.Config, name string) error {
	response, err := daemonRequest(cfg, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: name})
	if err != nil {
		return err
	}
	if err := responseError(response); err != nil {
		return err
	}

	var status protocol.StatusResponse
	if err := json.Unmarshal(response.Payload, &status); err != nil {
		return err
	}
	printStatus(status)
	return nil
}

func listUnitsCommand() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	response, err := daemonRequest(cfg, protocol.TypeListUnits, nil)
	if err != nil {
		return err
	}
	if err := responseError(response); err != nil {
		return err
	}

	var list protocol.ListUnitsResponse
	if err := json.Unmarshal(response.Payload, &list); err != nil {
		return err
	}
	printUnits(list)
	return nil
}

func daemonReloadCommand() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	response, err := daemonRequest(cfg, protocol.TypeDaemonReload, nil)
	if err != nil {
		return err
	}
	if err := responseError(response); err != nil {
		return err
	}

	var result protocol.ReloadResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		return err
	}
	if !result.Success {
		return errors.New("daemon reload failed")
	}
	return nil
}

func daemonRequest(cfg config.Config, msgType string, payload any) (transport.Message, error) {
	peerName := fmt.Sprintf("systemctl-%d", os.Getpid())
	tr, err := pipe.New(cfg.TransportConfig(), peerName, nil)
	if err != nil {
		return transport.Message{}, fmt.Errorf("create transport: %w", err)
	}
	defer tr.Close()

	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return transport.Message{}, err
		}
		raw = data
	}

	response, err := tr.Req(protocol.DaemonPeer, transport.Message{Type: msgType, Payload: raw})
	if err != nil {
		return transport.Message{}, fmt.Errorf("daemon is not available: %w", err)
	}
	return response, nil
}

func responseError(response transport.Message) error {
	if response.Type != protocol.TypeErrorResponse {
		return nil
	}
	var failure protocol.ErrorResponse
	if err := json.Unmarshal(response.Payload, &failure); err != nil {
		return err
	}
	return errors.New(failure.Error)
}

func waitStopped(manager scm.Manager, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		status, err := manager.QueryStatus(name)
		if err != nil {
			return err
		}
		if status.State == scm.StateStopped {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s to stop", name)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func printStatus(status protocol.StatusResponse) {
	fmt.Printf("%s - %s\n", status.Unit, status.SystemdState)
	if status.PID != 0 {
		fmt.Printf("    PID: %d\n", status.PID)
	}
	if status.ExitCode != 0 {
		fmt.Printf("    Exit code: %d\n", status.ExitCode)
	}
	if status.LastError != "" {
		fmt.Printf("    Error: %s\n", status.LastError)
	}
	for _, line := range status.LogTail {
		fmt.Println(line)
	}
}

func printUnits(list protocol.ListUnitsResponse) {
	fmt.Printf("%-24s %-24s %s\n", "UNIT", "STATE", "DESCRIPTION")
	for _, entry := range list.Units {
		fmt.Printf("%-24s %-24s %s\n", entry.Unit, entry.SystemdState, entry.Description)
	}
}

func vsystemdPath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	path := filepath.Join(filepath.Dir(executable), "vsystemd.exe")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("vsystemd executable not found at %s: %w", path, err)
	}
	return path, nil
}

func unitDependencies(file *unit.UnitFile) []string {
	seen := make(map[string]struct{})
	var dependencies []string
	for _, names := range [][]string{file.Requires, file.After} {
		for _, name := range names {
			if !strings.HasSuffix(name, ".service") {
				continue
			}
			dependency := strings.TrimSuffix(name, ".service")
			if _, ok := seen[dependency]; ok {
				continue
			}
			seen[dependency] = struct{}{}
			dependencies = append(dependencies, dependency)
		}
	}
	return dependencies
}

func unitUser(user, serviceName string) string {
	if user != "" {
		return user
	}
	return `NT SERVICE\` + serviceName
}
