package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/unit"
)

func runLoggedProcess(t *testing.T, service *unit.ServiceUnit, streamer *logStreamer) {
	t.Helper()
	parsed, err := parseCommand(service.ExecStart)
	if err != nil {
		t.Fatalf("parseCommand() error = %v", err)
	}
	cmd := exec.Command(parsed.path, parsed.args...)
	cmd.Stdout = streamer.Stdout()
	cmd.Stderr = streamer.Stderr()
	configureCommand(cmd)

	results := make(chan opResult, 1)
	op := newProcessOperation(roleMain, cmd, func(r opResult) { results <- r })
	if err := op.start(); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	if result := recvOpResult(t, results); result.err != nil {
		t.Fatalf("process error = %v", result.err)
	}
}

func TestLogStreamerDefault(t *testing.T) {
	logsDir := t.TempDir()

	service := &unit.ServiceUnit{
		ExecStart: helperCommand("output", "out-line", "err-line"),
	}
	streamer, err := newLogStreamer("app", service, logsDir, logwriter.Rotation{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("newLogStreamer() error = %v", err)
	}
	defer streamer.Close()

	runLoggedProcess(t, service, streamer)
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
	logsDir := t.TempDir()
	custom := filepath.Join(t.TempDir(), "custom.log")

	service := &unit.ServiceUnit{
		StandardOutput: "file:" + custom,
		ExecStart:      helperCommand("output", "stdout-text", "stderr-text"),
	}
	streamer, err := newLogStreamer("app", service, logsDir, logwriter.Rotation{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("newLogStreamer() error = %v", err)
	}
	defer streamer.Close()

	runLoggedProcess(t, service, streamer)
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
