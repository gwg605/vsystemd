package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/unit"
)

func newTestStorage(t *testing.T) (storage.Storage, storage.Paths) {
	t.Helper()
	paths := storage.PathsFromRoot(t.TempDir())
	store, err := storage.New(paths)
	if err != nil {
		t.Fatalf("storage.New() error = %v", err)
	}
	return store, paths
}

func TestLogStreamerStorage(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	store, paths := newTestStorage(t)

	service := &unit.ServiceUnit{
		ExecStart: helperCommand("output", "out-line", "err-line"),
	}
	streamer, err := NewLogStreamer("app", service, store)
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

	lines, err := store.ReadLogTail("app", 10)
	if err != nil {
		t.Fatalf("ReadLogTail() error = %v", err)
	}
	if !reflect.DeepEqual(lines, []string{"out-line"}) {
		t.Errorf("stdout = %v", lines)
	}

	stderr, err := os.ReadFile(filepath.Join(paths.LogsDir, "app.stderr"))
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if string(stderr) != "err-line" {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestLogStreamerFileRedirect(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	store, paths := newTestStorage(t)
	custom := filepath.Join(t.TempDir(), "custom.log")

	service := &unit.ServiceUnit{
		StandardOutput: "file:" + custom,
		ExecStart:      helperCommand("output", "stdout-text", "stderr-text"),
	}
	streamer, err := NewLogStreamer("app", service, store)
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

	lines, err := store.ReadLogTail("app", 10)
	if err != nil {
		t.Fatalf("ReadLogTail() error = %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("stdout = %v, want empty", lines)
	}

	stderr, err := os.ReadFile(filepath.Join(paths.LogsDir, "app.stderr"))
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if string(stderr) != "stderr-text" {
		t.Errorf("stderr = %q", stderr)
	}
}
