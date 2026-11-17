package storage

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestTimerStateStoredInUTC(t *testing.T) {
	st, paths := newTestStorage(t)
	zone := time.FixedZone("MSK", 3*60*60)
	input := TimerState{
		Enabled: true,
		LastRun: time.Date(2026, 11, 9, 10, 0, 0, 0, zone),
		NextRun: time.Date(2026, 11, 9, 11, 0, 0, 0, zone),
	}

	if err := st.WriteTimerState("job.timer", input); err != nil {
		t.Fatalf("WriteTimerState() error = %v", err)
	}
	got, err := st.ReadTimerState("job.timer")
	if err != nil {
		t.Fatalf("ReadTimerState() error = %v", err)
	}
	if got.LastRun.Location() != time.UTC || got.NextRun.Location() != time.UTC {
		t.Errorf("locations = %v/%v", got.LastRun.Location(), got.NextRun.Location())
	}
	if !got.LastRun.Equal(input.LastRun) || !got.NextRun.Equal(input.NextRun) {
		t.Errorf("times changed: %+v", got)
	}

	data, err := os.ReadFile(paths.TimersFile)
	if err != nil {
		t.Fatalf("read timer file: %v", err)
	}
	if !strings.Contains(string(data), "Z\"") {
		t.Errorf("timer file has no UTC timestamps: %s", data)
	}
}

func TestTimerStateWriteLeavesNoTempFile(t *testing.T) {
	st, paths := newTestStorage(t)

	if err := st.WriteTimerState("job.timer", TimerState{Enabled: true}); err != nil {
		t.Fatalf("WriteTimerState() error = %v", err)
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

func TestTimerStateWriteReplacesExisting(t *testing.T) {
	st, _ := newTestStorage(t)

	first := TimerState{Enabled: true, LastRun: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)}
	if err := st.WriteTimerState("job.timer", first); err != nil {
		t.Fatalf("WriteTimerState() error = %v", err)
	}
	second := TimerState{Enabled: true, LastRun: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
	if err := st.WriteTimerState("job.timer", second); err != nil {
		t.Fatalf("WriteTimerState() error = %v", err)
	}

	got, err := st.ReadTimerState("job.timer")
	if err != nil {
		t.Fatalf("ReadTimerState() error = %v", err)
	}
	if got != second {
		t.Errorf("ReadTimerState() = %+v, want %+v", got, second)
	}
}

func TestNewRequiresPaths(t *testing.T) {
	if _, err := New(Paths{}); err == nil {
		t.Error("New() error = nil for empty paths")
	}
}
