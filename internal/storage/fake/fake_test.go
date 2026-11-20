package fake

import (
	"bytes"
	"reflect"
	"testing"
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

func TestTimers(t *testing.T) {
	st := New()

	data, err := st.ReadTimers()
	if err != nil {
		t.Fatalf("ReadTimers() error = %v", err)
	}
	if data != nil {
		t.Errorf("ReadTimers() = %q for empty store, want nil", data)
	}

	payload := []byte(`{"job.timer":{"enabled":true}}`)
	if err := st.WriteTimers(payload); err != nil {
		t.Fatalf("WriteTimers() error = %v", err)
	}
	got, err := st.ReadTimers()
	if err != nil {
		t.Fatalf("ReadTimers() error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("ReadTimers() = %q, want %q", got, payload)
	}
}
