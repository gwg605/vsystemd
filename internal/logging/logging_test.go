package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureWritesLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vsystemd.log")
	if err := Configure(path, "debug"); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	defer Close()

	slog.Debug("test message", "key", "value")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "test message") || !strings.Contains(string(data), "key=value") {
		t.Errorf("log = %q", data)
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
