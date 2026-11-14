package fake

import (
	"reflect"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/storage"
)

func TestUnits(t *testing.T) {
	st := New()
	st.WriteUnit("job.timer", []byte("[Timer]\nOnCalendar=daily\n"))
	st.WriteUnit("app.service", []byte("[Service]\nExecStart=/app\n"))
	st.WriteUnit("notes.txt", []byte("ignored\n"))

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
	if string(data) != "[Service]\nExecStart=/app\n" {
		t.Errorf("ReadUnit() = %q", data)
	}

	if _, err := st.ReadUnit("missing.service"); err == nil {
		t.Error("ReadUnit() error = nil for missing unit")
	}
}

func TestTimerStates(t *testing.T) {
	st := New()
	state := storage.TimerState{
		Enabled: true,
		LastRun: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC),
		NextRun: time.Date(2026, 11, 9, 11, 0, 0, 0, time.UTC),
	}

	got, err := st.ReadTimerState("job.timer")
	if err != nil {
		t.Fatalf("ReadTimerState() error = %v", err)
	}
	if got != (storage.TimerState{}) {
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
	if !reflect.DeepEqual(states, map[string]storage.TimerState{"job.timer": state}) {
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
