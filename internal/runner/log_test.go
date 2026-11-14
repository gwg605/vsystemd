package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/unit"
)

func TestLogStreamerDefault(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	logsDir := t.TempDir()

	service := &unit.ServiceUnit{
		ExecStart: helperCommand("output", "out-line", "err-line"),
	}
	streamer, err := NewLogStreamer("app", service, logsDir, logwriter.Rotation{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer streamer.Close()

	manager := NewProcessManager(service)
	manager.SetLogStreamer(streamer)
	if err := manager.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := manager.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if err := streamer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	path, err := logwriter.LatestFile(logwriter.ServiceLogDir(logsDir, "app"))
	if err != nil {
		t.Fatalf("LatestFile() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "out-line") || !strings.Contains(string(data), "err-line") {
		t.Errorf("log = %q", data)
	}
}

func TestLogStreamerFileRedirect(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	logsDir := t.TempDir()
	custom := filepath.Join(t.TempDir(), "custom.log")

	service := &unit.ServiceUnit{
		StandardOutput: "file:" + custom,
		ExecStart:      helperCommand("output", "stdout-text", "stderr-text"),
	}
	streamer, err := NewLogStreamer("app", service, logsDir, logwriter.Rotation{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer streamer.Close()

	manager := NewProcessManager(service)
	manager.SetLogStreamer(streamer)
	if err := manager.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := manager.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if err := streamer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	data, err := os.ReadFile(custom)
	if err != nil {
		t.Fatalf("read custom log: %v", err)
	}
	if string(data) != "stdout-text" {
		t.Errorf("custom log = %q", data)
	}

	path, err := logwriter.LatestFile(logwriter.ServiceLogDir(logsDir, "app"))
	if err != nil {
		t.Fatalf("LatestFile() error = %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read default log: %v", err)
	}
	if string(data) != "stderr-text" {
		t.Errorf("default log = %q", data)
	}
}
