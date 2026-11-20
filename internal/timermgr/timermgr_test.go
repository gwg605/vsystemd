package timermgr

import (
	"errors"
	"testing"
	"time"

	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
)

func newManager(t *testing.T, onEvent EventFunc) (*Manager, *storagefake.Storage) {
	t.Helper()
	store := storagefake.New()
	m, err := New(store, onEvent)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = m.Stop() })
	return m, store
}

func TestSetTimerInvalid(t *testing.T) {
	m, _ := newManager(t, nil)

	if err := m.SetTimer(TimerConfig{OnCalendar: []string{"daily"}}); err == nil {
		t.Error("SetTimer() error = nil without name")
	}
	if err := m.SetTimer(TimerConfig{Name: "job.timer"}); err == nil {
		t.Error("SetTimer() error = nil without OnCalendar")
	}
	if err := m.SetTimer(TimerConfig{Name: "job.timer", OnCalendar: []string{"not a calendar event"}}); err == nil {
		t.Error("SetTimer() error = nil for invalid OnCalendar")
	}
}

func TestFireNotifies(t *testing.T) {
	events := make(chan TimerStatus, 1)
	m, store := newManager(t, func(_ string, status TimerStatus) {
		select {
		case events <- status:
		default:
		}
	})

	if err := m.SetTimer(TimerConfig{Name: "job.timer", OnCalendar: []string{"*-*-* *:*:*"}}); err != nil {
		t.Fatalf("SetTimer() error = %v", err)
	}
	if err := m.StartTimer("job.timer"); err != nil {
		t.Fatalf("StartTimer() error = %v", err)
	}

	select {
	case status := <-events:
		if status.Name != "job.timer" || !status.Running || status.LastRun.IsZero() {
			t.Errorf("event status = %+v", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timer did not fire")
	}

	status, err := m.QueryStatus("job.timer")
	if err != nil {
		t.Fatalf("QueryStatus() error = %v", err)
	}
	if status.LastRun.IsZero() {
		t.Error("last run was not recorded")
	}

	// The timer database was persisted.
	if data, err := store.ReadTimers(); err != nil || len(data) == 0 {
		t.Errorf("ReadTimers() = %q, %v", data, err)
	}
}

func TestStopClearsNextRun(t *testing.T) {
	m, _ := newManager(t, nil)

	if err := m.SetTimer(TimerConfig{Name: "job.timer", OnCalendar: []string{"daily"}}); err != nil {
		t.Fatalf("SetTimer() error = %v", err)
	}
	if err := m.StartTimer("job.timer"); err != nil {
		t.Fatalf("StartTimer() error = %v", err)
	}
	started, _ := m.QueryStatus("job.timer")
	if started.NextRun.IsZero() || started.ActiveSince.IsZero() {
		t.Fatalf("started status = %+v", started)
	}

	if err := m.StopTimer("job.timer"); err != nil {
		t.Fatalf("StopTimer() error = %v", err)
	}
	stopped, _ := m.QueryStatus("job.timer")
	if stopped.Running {
		t.Error("timer is still running after stop")
	}
	if !stopped.NextRun.IsZero() || !stopped.ActiveSince.IsZero() {
		t.Errorf("stop did not clear runtime state: %+v", stopped)
	}
}

func TestDeleteGuards(t *testing.T) {
	m, _ := newManager(t, nil)

	if err := m.DeleteTimer("missing.timer"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteTimer() error = %v, want ErrNotFound", err)
	}

	if err := m.SetTimer(TimerConfig{Name: "job.timer", OnCalendar: []string{"daily"}}); err != nil {
		t.Fatalf("SetTimer() error = %v", err)
	}
	if err := m.Enable("job.timer", true); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if err := m.DeleteTimer("job.timer"); err == nil {
		t.Error("DeleteTimer() error = nil for enabled timer")
	}
	if err := m.Enable("job.timer", false); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if err := m.StartTimer("job.timer"); err != nil {
		t.Fatalf("StartTimer() error = %v", err)
	}
	if err := m.DeleteTimer("job.timer"); err == nil {
		t.Error("DeleteTimer() error = nil for running timer")
	}
	if err := m.StopTimer("job.timer"); err != nil {
		t.Fatalf("StopTimer() error = %v", err)
	}
	if err := m.DeleteTimer("job.timer"); err != nil {
		t.Errorf("DeleteTimer() error = %v for stopped, disabled timer", err)
	}
}

func TestReloadSchedulesEnabled(t *testing.T) {
	store := storagefake.New()
	m, err := New(store, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := m.SetTimer(TimerConfig{Name: "job.timer", OnCalendar: []string{"daily"}}); err != nil {
		t.Fatalf("SetTimer() error = %v", err)
	}
	if err := m.Enable("job.timer", true); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	reloaded, err := New(store, nil)
	if err != nil {
		t.Fatalf("New(reload) error = %v", err)
	}
	defer reloaded.Stop()
	if err := reloaded.Start(); err != nil {
		t.Fatalf("Start(reload) error = %v", err)
	}
	status, err := reloaded.QueryStatus("job.timer")
	if err != nil {
		t.Fatalf("QueryStatus() error = %v", err)
	}
	if !status.Enabled || !status.Running {
		t.Errorf("enabled timer was not scheduled on start: %+v", status)
	}
}

func TestSetTimerReschedulesOnChange(t *testing.T) {
	m, _ := newManager(t, nil)

	if err := m.SetTimer(TimerConfig{Name: "job.timer", OnCalendar: []string{"daily"}}); err != nil {
		t.Fatalf("SetTimer() error = %v", err)
	}
	if err := m.StartTimer("job.timer"); err != nil {
		t.Fatalf("StartTimer() error = %v", err)
	}
	before, _ := m.QueryStatus("job.timer")

	if err := m.SetTimer(TimerConfig{Name: "job.timer", OnCalendar: []string{"weekly"}}); err != nil {
		t.Fatalf("SetTimer(change) error = %v", err)
	}
	after, _ := m.QueryStatus("job.timer")
	if !after.Running {
		t.Error("timer stopped after config change")
	}
	if after.NextRun.Equal(before.NextRun) {
		t.Errorf("next run was not recomputed: %v", after.NextRun)
	}
	cfg, err := m.GetTimer("job.timer")
	if err != nil || len(cfg.OnCalendar) != 1 || cfg.OnCalendar[0] != "weekly" {
		t.Errorf("GetTimer() = %+v, %v", cfg, err)
	}
}

func TestNextEventLocalTimezone(t *testing.T) {
	events, err := parseEvents("job.timer", []string{"*-*-* 12:00:00"})
	if err != nil {
		t.Fatalf("parseEvents() error = %v", err)
	}

	zone := time.FixedZone("MSK", 3*60*60)
	now := time.Date(2026, 11, 11, 11, 0, 0, 0, zone)
	next, ok := nextEvent(events, now)
	if !ok {
		t.Fatal("nextEvent() = false")
	}

	want := time.Date(2026, 11, 11, 12, 0, 0, 0, zone).UTC()
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
	if next.Location() != time.UTC {
		t.Errorf("next location = %v", next.Location())
	}
}

func TestNextEventExplicitTimezone(t *testing.T) {
	events, err := parseEvents("job.timer", []string{"*-*-* 12:00:00 Asia/Tokyo"})
	if err != nil {
		t.Fatalf("parseEvents() error = %v", err)
	}

	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	next, ok := nextEvent(events, now)
	if !ok {
		t.Fatal("nextEvent() = false")
	}

	want := time.Date(2026, 11, 11, 3, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}
