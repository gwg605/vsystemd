package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akamensky/argparse"

	vsystemd "github.com/gwg605/vsystemd"
	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/logwriter"
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

	if enableCmd.Happened() {
		if *enableUnit == "" {
			return errors.New(parser.Usage("unit name is required"))
		}
		id, err := parseUnit(*enableUnit)
		if err != nil {
			return err
		}
		if id.Type == unit.TypeTimer {
			return timerCommand(protocol.TypeTimerEnable, protocol.TimerRequest{Unit: id.String(), Enabled: true})
		}
		return enableService(id, *password, *passwordFile, *passwordStdin)
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
			return dispatch(command.name, id)
		}
	}

	switch {
	case listUnits.Happened():
		return listUnitsCommand()
	case listTimers.Happened():
		return listTimersCommand(*listTimersAll)
	case daemonReload.Happened():
		return daemonReloadCommand()
	default:
		return errors.New(parser.Usage("command is required"))
	}
}

func parseUnit(name string) (unit.ID, error) {
	if filepath.Ext(name) == "" {
		name += unit.TypeService.Extension()
	}
	return unit.ParseID(name)
}

func dispatch(command string, id unit.ID) error {
	if id.Type == unit.TypeTimer {
		return dispatchTimer(command, id)
	}

	manager := scm.New()
	serviceName := id.Name

	if serviceName == config.ManagerServiceName {
		return fmt.Errorf("service %s is the vsystemd manager and cannot be controlled by systemctl", serviceName)
	}

	switch command {
	case "disable":
		if _, err := manager.QueryStatus(serviceName); err != nil {
			return nil
		}
		if err := manager.StopService(serviceName); err != nil {
			return err
		}
		if err := waitStopped(manager, serviceName, 2*time.Minute); err != nil {
			return err
		}
		return manager.DeleteService(serviceName)
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
		return statusCommand(cfg, id)
	default:
		return fmt.Errorf("unknown command %s", command)
	}
}

func dispatchTimer(command string, id unit.ID) error {
	switch command {
	case "enable":
		return timerCommand(protocol.TypeTimerEnable, protocol.TimerRequest{Unit: id.String(), Enabled: true})
	case "disable":
		return timerCommand(protocol.TypeTimerEnable, protocol.TimerRequest{Unit: id.String(), Enabled: false})
	case "start":
		return timerCommand(protocol.TypeTimerStart, protocol.TimerRequest{Unit: id.String()})
	case "stop":
		return timerCommand(protocol.TypeTimerStop, protocol.TimerRequest{Unit: id.String()})
	case "status":
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		return statusCommand(cfg, id)
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

func enableService(id unit.ID, password, passwordFile string, passwordStdin bool) error {
	if id.Name == config.ManagerServiceName {
		return fmt.Errorf("service %s is the vsystemd manager and cannot be enabled by systemctl", config.ManagerServiceName)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	store, err := storage.New(cfg.Paths())
	if err != nil {
		return err
	}
	data, err := store.ReadUnit(id.String())
	if err != nil {
		return err
	}
	parsed, err := unit.Parse(id, data)
	if err != nil {
		return err
	}
	for _, warning := range parsed.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s: %s\n", id, warning)
	}
	for _, warning := range unitPathWarnings(parsed) {
		fmt.Fprintf(os.Stderr, "warning: %s: %s\n", id, warning)
	}

	executable, err := vsystemdPath()
	if err != nil {
		return err
	}
	resolvedPassword, err := resolvePassword(password, passwordFile, passwordStdin)
	if err != nil {
		return err
	}

	serviceConfig := scm.NewServiceConfig(parsed, executable, scm.StartTypeAutomatic)
	serviceConfig.SetPassword(resolvedPassword)

	manager := scm.New()
	if _, err := manager.QueryStatus(id.Name); err != nil {
		return manager.CreateService(serviceConfig)
	}
	return manager.UpdateService(serviceConfig)
}

// unitPathWarnings checks the custom output and environment file paths of a
// service unit and returns warnings for invalid, missing, or inaccessible
// paths. It never fails: problems are reported to the user as warnings.
func unitPathWarnings(parsed *unit.UnitFile) []string {
	var warnings []string

	stdout, stderr, err := logwriter.ResolveServiceOutputs(parsed.Service)
	if err != nil {
		warnings = append(warnings, err.Error())
	} else {
		for _, target := range []logwriter.OutputTarget{stdout, stderr} {
			if target.Kind != logwriter.OutputFile && target.Kind != logwriter.OutputAppend {
				continue
			}
			if err := logwriter.CheckOutputWritable(target.Path); err != nil {
				warnings = append(warnings, fmt.Sprintf("output %s: %v", target.Path, err))
			}
		}
	}

	for _, file := range parsed.Service.EnvironmentFiles {
		optional := strings.HasPrefix(file, "-")
		path := strings.TrimPrefix(file, "-")
		if err := logwriter.ValidateUnitPath(path); err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if err := logwriter.CheckEnvReadable(path); err != nil {
			if optional && os.IsNotExist(err) {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("environment file %s: %v", path, err))
		}
	}
	return warnings
}

func statusCommand(cfg config.Config, id unit.ID) error {
	response, err := daemonRequest(cfg, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: id.String()})
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
	if id.Type == unit.TypeTimer {
		printTimerStatus(cfg, id, status)
	} else {
		printServiceStatus(cfg, id, status)
	}
	return nil
}

func listUnitsCommand() error {
	list, err := fetchUnits()
	if err != nil {
		return err
	}
	printUnits(list)
	return nil
}

func listTimersCommand(all bool) error {
	list, err := fetchUnits()
	if err != nil {
		return err
	}
	printTimers(list, all)
	return nil
}

func fetchUnits() (protocol.ListUnitsResponse, error) {
	cfg, err := config.Load()
	if err != nil {
		return protocol.ListUnitsResponse{}, err
	}
	response, err := daemonRequest(cfg, protocol.TypeListUnits, nil)
	if err != nil {
		return protocol.ListUnitsResponse{}, err
	}
	if err := responseError(response); err != nil {
		return protocol.ListUnitsResponse{}, err
	}

	var list protocol.ListUnitsResponse
	if err := json.Unmarshal(response.Payload, &list); err != nil {
		return protocol.ListUnitsResponse{}, err
	}
	return list, nil
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
	local := transport.NewPeer(transport.PeerControl, fmt.Sprintf("systemctl_%d", os.Getpid()), false)
	tr, err := pipe.New(cfg.TransportConfig(), local, nil, nil)
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

	slog.Debug("systemctl: sending request", "type", msgType, "local", local.String())
	response, err := tr.Req(tr.Target(transport.PeerDaemon), transport.Message{Type: msgType, Payload: raw})
	if err != nil {
		slog.Error("systemctl: request failed", "type", msgType, "error", err)
		return transport.Message{}, fmt.Errorf("daemon is not available: %w", err)
	}
	slog.Debug("systemctl: response received", "type", response.Type, "request_id", response.RequestID)
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

func printServiceStatus(cfg config.Config, id unit.ID, status protocol.StatusResponse) {
	for _, line := range serviceStatusLines(cfg, id, status) {
		fmt.Println(line)
	}
}

func serviceStatusLines(cfg config.Config, id unit.ID, status protocol.StatusResponse) []string {
	header := fmt.Sprintf("● %s", status.Unit)
	if status.Description != "" {
		header += " - " + status.Description
	}

	loaded := filepath.Join(cfg.Paths().SystemDir, id.String())
	if status.Enabled != nil {
		loaded += "; " + enabledDisabled(*status.Enabled)
	}

	active := status.SystemdState
	if status.SystemdState != "inactive (dead)" && !status.LastTransition.IsZero() {
		active += fmt.Sprintf(" since %s; %s ago", formatTime(status.LastTransition), formatDelta(time.Since(status.LastTransition)))
	}

	lines := []string{
		header,
		fmt.Sprintf("%12s %s", "Loaded:", "loaded ("+loaded+")"),
		fmt.Sprintf("%12s %s", "Active:", active),
	}
	if status.PID != 0 {
		lines = append(lines, fmt.Sprintf("%12s %d", "Main PID:", status.PID))
	}
	if status.ExitCode != 0 {
		lines = append(lines, fmt.Sprintf("%12s %d", "Exit code:", status.ExitCode))
	}
	if status.RestartCount > 0 {
		lines = append(lines, fmt.Sprintf("%12s %d", "Restart:", status.RestartCount))
	}
	if status.LastError != "" {
		lines = append(lines, fmt.Sprintf("%12s %s", "Error:", status.LastError))
	}
	if len(status.LogTail) > 0 {
		lines = append(lines, "")
		lines = append(lines, status.LogTail...)
	}
	return lines
}

func printTimerStatus(cfg config.Config, id unit.ID, status protocol.StatusResponse) {
	for _, line := range timerStatusLines(cfg, id, status) {
		fmt.Println(line)
	}
}

func timerStatusLines(cfg config.Config, id unit.ID, status protocol.StatusResponse) []string {
	header := fmt.Sprintf("● %s", status.Unit)
	if status.Description != "" {
		header += " - " + status.Description
	}

	loaded := filepath.Join(cfg.Paths().SystemDir, id.String())
	if status.Enabled != nil {
		loaded += "; " + enabledDisabled(*status.Enabled)
	}

	active := status.SystemdState
	if status.ActiveSince != nil {
		active += fmt.Sprintf(" since %s; %s ago", formatTime(*status.ActiveSince), formatDelta(time.Since(*status.ActiveSince)))
	}

	lines := []string{
		header,
		fmt.Sprintf("%12s %s", "Loaded:", "loaded ("+loaded+")"),
		fmt.Sprintf("%12s %s", "Active:", active),
	}
	if status.NextRun != nil {
		lines = append(lines, fmt.Sprintf("%12s %s; %s left", "Trigger:", formatTime(*status.NextRun), formatDelta(time.Until(*status.NextRun))))
	}
	if status.Activates != "" {
		lines = append(lines, fmt.Sprintf("%12s ● %s", "Triggers:", status.Activates))
	}
	if len(status.LogTail) > 0 {
		lines = append(lines, "")
		lines = append(lines, status.LogTail...)
	}
	return lines
}

func enabledDisabled(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}

func printUnits(list protocol.ListUnitsResponse) {
	fmt.Printf("%-24s %-24s %s\n", "UNIT", "STATE", "DESCRIPTION")
	for _, entry := range list.Units {
		fmt.Printf("%-24s %-24s %s\n", entry.Unit, entry.SystemdState, entry.Description)
	}
}

func timerActive(entry protocol.UnitStatus) bool {
	return entry.Running != nil && *entry.Running
}

func printTimers(list protocol.ListUnitsResponse, all bool) {
	fmt.Printf("%-28s %-12s %-28s %-12s %-24s %s\n", "NEXT", "LEFT", "LAST", "PASSED", "UNIT", "ACTIVATES")
	for _, entry := range list.Units {
		if entry.Type != "timer" {
			continue
		}
		if !all && !timerActive(entry) {
			continue
		}

		var next, left, last, passed string
		if entry.NextRun != nil {
			next = formatTime(*entry.NextRun)
			left = formatDelta(time.Until(*entry.NextRun))
		}
		if entry.LastRun != nil {
			last = formatTime(*entry.LastRun)
			passed = formatDelta(time.Since(*entry.LastRun))
		}
		fmt.Printf("%-28s %-12s %-28s %-12s %-24s %s\n", next, left, last, passed, entry.Unit, entry.Activates)
	}
}

func formatTime(value time.Time) string {
	return value.Local().Format("2006-01-02 15:04:05 -07:00")
}

func formatDelta(value time.Duration) string {
	if value < 0 {
		value = -value
	}

	total := int64(value / time.Second)
	days := total / 86400
	total %= 86400
	hours := total / 3600
	total %= 3600
	minutes := total / 60
	seconds := total % 60

	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if len(parts) == 0 || seconds > 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	return strings.Join(parts, " ")
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

func resolvePassword(provided, passwordFile string, stdin bool) (string, error) {
	sources := 0
	if provided != "" {
		sources++
	}
	if passwordFile != "" {
		sources++
	}
	if stdin {
		sources++
	}
	if sources > 1 {
		return "", errors.New("specify only one of --password, --password-file, --password-stdin")
	}

	switch {
	case provided != "":
		return provided, nil
	case passwordFile != "":
		return readPasswordFile(passwordFile)
	case stdin:
		return readPassword(os.Stdin)
	default:
		return "", nil
	}
}

func readPasswordFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read password file: %w", err)
	}
	return trimPassword(string(data)), nil
}

func readPassword(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return trimPassword(string(data)), nil
}

func trimPassword(value string) string {
	return strings.TrimRight(value, "\r\n")
}
