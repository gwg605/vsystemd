package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/unit"
)

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
	if status.ConfigError != "" {
		active += " invalid"
	}
	if status.SystemdState != "inactive (dead)" && !status.LastTransition.IsZero() {
		active += fmt.Sprintf(" since %s; %s ago", formatTime(status.LastTransition), formatDelta(time.Since(status.LastTransition)))
	}

	lines := []string{
		header,
		fmt.Sprintf("%12s %s", "Loaded:", "loaded ("+loaded+")"),
		fmt.Sprintf("%12s %s", "Active:", active),
	}
	if status.ConfigError != "" {
		lines = append(lines, fmt.Sprintf("%12s %s", "Config:", status.ConfigError))
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
	if status.ConfigError != "" {
		active += " invalid"
	}
	if status.ActiveSince != nil {
		active += fmt.Sprintf(" since %s; %s ago", formatTime(*status.ActiveSince), formatDelta(time.Since(*status.ActiveSince)))
	}

	lines := []string{
		header,
		fmt.Sprintf("%12s %s", "Loaded:", "loaded ("+loaded+")"),
		fmt.Sprintf("%12s %s", "Active:", active),
	}
	if status.ConfigError != "" {
		lines = append(lines, fmt.Sprintf("%12s %s", "Config:", status.ConfigError))
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
		state := entry.SystemdState
		if entry.ConfigError != "" {
			state += " invalid"
		}
		fmt.Printf("%-24s %-24s %s\n", entry.Unit, state, entry.Description)
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
