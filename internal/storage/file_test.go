package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func newTestStorage(t *testing.T) (Storage, Paths) {
	t.Helper()
	paths := PathsFromRoot(t.TempDir())
	st, err := New(paths)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return st, paths
}

func TestFileUnits(t *testing.T) {
	st, paths := newTestStorage(t)

	files := map[string]string{
		"app.service": "[Service]\n",
		"job.timer":   "[Timer]\n",
		"notes.txt":   "ignored\n",
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(paths.SystemDir, name), []byte(data), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(paths.SystemDir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	units, err := st.ListUnits()
	if err != nil {
		t.Fatalf("ListUnits() error = %v", err)
	}
	if !reflect.DeepEqual(units, []string{"app.service", "job.timer"}) {
		t.Errorf("ListUnits() = %v", units)
	}

	data, err := st.ReadUnit("app.service")
	if err != nil {
		t.Fatalf("ReadUnit() error = %v", err)
	}
	if string(data) != "[Service]\n" {
		t.Errorf("ReadUnit() = %q", data)
	}

	if _, err := st.ReadUnit("missing.service"); err == nil {
		t.Error("ReadUnit() error = nil for missing unit")
	}
}

func TestFileTimers(t *testing.T) {
	st, _ := newTestStorage(t)

	data, err := st.ReadTimers()
	if err != nil {
		t.Fatalf("ReadTimers() error = %v", err)
	}
	if data != nil {
		t.Errorf("ReadTimers() = %q for missing file, want nil", data)
	}

	first := []byte(`{"job.timer":{"enabled":true}}`)
	if err := st.WriteTimers(first); err != nil {
		t.Fatalf("WriteTimers() error = %v", err)
	}
	got, err := st.ReadTimers()
	if err != nil {
		t.Fatalf("ReadTimers() error = %v", err)
	}
	if !bytes.Equal(got, first) {
		t.Errorf("ReadTimers() = %q, want %q", got, first)
	}

	second := []byte(`{"job.timer":{"enabled":false}}`)
	if err := st.WriteTimers(second); err != nil {
		t.Fatalf("WriteTimers() error = %v", err)
	}
	got, err = st.ReadTimers()
	if err != nil {
		t.Fatalf("ReadTimers() error = %v", err)
	}
	if !bytes.Equal(got, second) {
		t.Errorf("ReadTimers() = %q, want %q", got, second)
	}
}

func TestWriteTimersLeavesNoTempFile(t *testing.T) {
	st, paths := newTestStorage(t)

	if err := st.WriteTimers([]byte(`{}`)); err != nil {
		t.Fatalf("WriteTimers() error = %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(paths.TimersFile))
	if err != nil {
		t.Fatalf("read timer dir: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	if !reflect.DeepEqual(names, []string{"timers.json"}) {
		t.Errorf("timer dir = %v, want [timers.json]", names)
	}
}

func TestNewRequiresPaths(t *testing.T) {
	if _, err := New(Paths{}); err == nil {
		t.Error("New() error = nil for empty paths")
	}
}
