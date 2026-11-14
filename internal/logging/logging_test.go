package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestConfigureWritesLog(t *testing.T) {
	var buffer bytes.Buffer
	Configure(&buffer, "debug")

	slog.Debug("test message", "key", "value")

	output := buffer.String()
	if !strings.Contains(output, "test message") || !strings.Contains(output, "key=value") {
		t.Errorf("log = %q", output)
	}
	if strings.Contains(output, "time=") || strings.Contains(output, "level=") || strings.Contains(output, "msg=") {
		t.Errorf("log contains slog field prefixes: %q", output)
	}
	if !strings.Contains(output, "[DEBUG]") {
		t.Errorf("log has no level prefix: %q", output)
	}
	if !strings.Contains(output, "Z [DEBUG]") {
		t.Errorf("log timestamp is not UTC: %q", output)
	}
}

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"other":   slog.LevelInfo,
	}
	for input, want := range tests {
		if got := parseLevel(input); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", input, got, want)
		}
	}
}
