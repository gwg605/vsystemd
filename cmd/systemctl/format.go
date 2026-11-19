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
	if status.SystemdState != "inactive (dead)" && !status.LastTransition.IsZero() {
		active += fmt.Sprintf(" since %s; %s ago", formatTime(status.LastTransition), formatDelta(time.Since(status.LastTransition)))
	}

	lines := []string{
		header,
		fmt.Sprintf("%12s %s", "Loaded:", loadedLine(status.LoadState, loaded, status.LoadError)),
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
		fmt.Sprintf("%12s %s", "Loaded:", loadedLine(status.LoadState, loaded, status.LoadError)),
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

func loadedLine(state, detail, reason string) string {
	if state == "" || state == "loaded" {
		return "loaded (" + detail + ")"
	}
	if reason != "" {
		return state + " (Reason: " + reason + ")"
	}
	return state
}

func printUnits(list protocol.ListUnitsResponse) {
	fmt.Printf("%-28s %-10s %-9s %-9s %s\n", "UNIT", "LOAD", "ACTIVE", "SUB", "DESCRIPTION")
	for _, entry := range list.Units {
		active, sub := splitState(entry.SystemdState)
		load := entry.LoadState
		if load == "" {
			load = "loaded"
		}
		unit := entry.Unit
		if active == "failed" || load != "loaded" {
			unit = "● " + unit
		}
		fmt.Printf("%-28s %-10s %-9s %-9s %s\n", unit, load, active, sub, entry.Description)
	}
}

func splitState(state string) (string, string) {
	if open := strings.Index(state, " ("); open >= 0 && strings.HasSuffix(state, ")") {
		return state[:open], state[open+2 : len(state)-1]
	}
	return state, state
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
