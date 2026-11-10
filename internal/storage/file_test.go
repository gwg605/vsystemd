package storage

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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
	if _, err := st.ReadUnit("../app.service"); err == nil {
		t.Error("ReadUnit() error = nil for invalid name")
	}
}

func TestFileLogs(t *testing.T) {
	st, _ := newTestStorage(t)

	if err := st.WriteLog("app", "stdout", []byte("one\ntwo\n")); err != nil {
		t.Fatalf("WriteLog() error = %v", err)
	}
	if err := st.WriteLog("app", "stdout", []byte("three\n")); err != nil {
		t.Fatalf("WriteLog() error = %v", err)
	}
	if err := st.WriteLog("app", "stderr", []byte("error\n")); err != nil {
		t.Fatalf("WriteLog() error = %v", err)
	}

	lines, err := st.ReadLogTail("app", 2)
	if err != nil {
		t.Fatalf("ReadLogTail() error = %v", err)
	}
	if !reflect.DeepEqual(lines, []string{"two", "three"}) {
		t.Errorf("ReadLogTail() = %v", lines)
	}

	missing, err := st.ReadLogTail("missing", 10)
	if err != nil {
		t.Fatalf("ReadLogTail() error = %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("ReadLogTail() = %v for missing log", missing)
	}

	if err := st.WriteLog("../app", "stdout", nil); err == nil {
		t.Error("WriteLog() error = nil for invalid name")
	}
}

func TestFileTimerStates(t *testing.T) {
	st, _ := newTestStorage(t)
	state := TimerState{
		Enabled: true,
		LastRun: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC),
		NextRun: time.Date(2026, 11, 9, 11, 0, 0, 0, time.UTC),
	}

	got, err := st.ReadTimerState("job.timer")
	if err != nil {
		t.Fatalf("ReadTimerState() error = %v", err)
	}
	if got != (TimerState{}) {
		t.Errorf("ReadTimerState() = %+v for missing timer", got)
	}

	if err := st.WriteTimerState("job.timer", state); err != nil {
		t.Fatalf("WriteTimerState() error = %v", err)
	}
	got, err = st.ReadTimerState("job.timer")
	if err != nil {
		t.Fatalf("ReadTimerState() error = %v", err)
	}
	if got != state {
		t.Errorf("ReadTimerState() = %+v", got)
	}

	states, err := st.ListTimerStates()
	if err != nil {
		t.Fatalf("ListTimerStates() error = %v", err)
	}
	if !reflect.DeepEqual(states, map[string]TimerState{"job.timer": state}) {
		t.Errorf("ListTimerStates() = %v", states)
	}

	if err := st.DeleteTimerState("job.timer"); err != nil {
		t.Fatalf("DeleteTimerState() error = %v", err)
	}
	states, err = st.ListTimerStates()
	if err != nil {
		t.Fatalf("ListTimerStates() error = %v", err)
	}
	if len(states) != 0 {
		t.Errorf("ListTimerStates() = %v after delete", states)
	}
}

func TestNewRequiresPaths(t *testing.T) {
	if _, err := New(Paths{}); err == nil {
		t.Error("New() error = nil for empty paths")
	}
}
