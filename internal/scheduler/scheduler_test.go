package scheduler

import (
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/scm"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
)

func TestAddInvalid(t *testing.T) {
	scheduler := New(scmfake.New(), storagefake.New())
	defer scheduler.Close()

	if err := scheduler.Add(Timer{Name: "job.timer", Service: "app"}); err == nil {
		t.Error("Add() error = nil without OnCalendar")
	}
	if err := scheduler.Add(Timer{
		Name:       "job.timer",
		Service:    "app",
		OnCalendar: []string{"not a calendar event"},
	}); err == nil {
		t.Error("Add() error = nil for invalid OnCalendar")
	}
}

func TestSchedulerFires(t *testing.T) {
	store := storagefake.New()
	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "app"}); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}

	scheduler := New(manager, store)
	defer scheduler.Close()

	if err := scheduler.Add(Timer{
		Name:       "job.timer",
		Service:    "app",
		OnCalendar: []string{"*-*-* *:*:*"},
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		status, err := manager.QueryStatus("app")
		if err == nil && status.State == scm.StateRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("service was not started by scheduler")
		}
		time.Sleep(20 * time.Millisecond)
	}

	for {
		state, err := store.ReadTimerState("job.timer")
		if err != nil {
			t.Fatalf("ReadTimerState() error = %v", err)
		}
		if !state.LastRun.IsZero() && !state.NextRun.IsZero() {
			if state.LastRun.Location() != time.UTC || state.NextRun.Location() != time.UTC {
				t.Errorf("timer times are not UTC: %v/%v", state.LastRun.Location(), state.NextRun.Location())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timer state was not persisted")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStopClearsNextRun(t *testing.T) {
	store := storagefake.New()
	scheduler := New(scmfake.New(), store)
	defer scheduler.Close()

	if err := scheduler.Add(Timer{
		Name:       "job.timer",
		Service:    "app",
		OnCalendar: []string{"daily"},
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	state, err := store.ReadTimerState("job.timer")
	if err != nil {
		t.Fatalf("ReadTimerState() error = %v", err)
	}
	if state.NextRun.IsZero() {
		t.Fatal("next run was not set")
	}
	lastRun := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	state.LastRun = lastRun
	if err := store.WriteTimerState("job.timer", state); err != nil {
		t.Fatalf("WriteTimerState() error = %v", err)
	}

	if err := scheduler.Stop("job.timer"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	state, err = store.ReadTimerState("job.timer")
	if err != nil {
		t.Fatalf("ReadTimerState() error = %v", err)
	}
	if !state.NextRun.IsZero() {
		t.Errorf("NextRun = %v, want zero", state.NextRun)
	}
	if !state.LastRun.Equal(lastRun) {
		t.Errorf("LastRun = %v, want %v", state.LastRun, lastRun)
	}
}

func TestNextEventLocalTimezone(t *testing.T) {
	events, err := parseEvents(Timer{
		Name:       "job.timer",
		Service:    "app",
		OnCalendar: []string{"*-*-* 12:00:00"},
	})
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
	events, err := parseEvents(Timer{
		Name:       "job.timer",
		Service:    "app",
		OnCalendar: []string{"*-*-* 12:00:00 Asia/Tokyo"},
	})
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

func TestReloadDiff(t *testing.T) {
	store := storagefake.New()
	scheduler := New(scmfake.New(), store)
	defer scheduler.Close()

	if err := scheduler.Add(Timer{Name: "a.timer", Service: "app", OnCalendar: []string{"daily"}}); err != nil {
		t.Fatalf("Add(a) error = %v", err)
	}
	if err := scheduler.Add(Timer{Name: "b.timer", Service: "app", OnCalendar: []string{"daily"}}); err != nil {
		t.Fatalf("Add(b) error = %v", err)
	}

	scheduler.mu.Lock()
	oldA := scheduler.timers["a.timer"]
	scheduler.mu.Unlock()

	err := scheduler.Reload([]Timer{
		{Name: "a.timer", Service: "app", OnCalendar: []string{"weekly"}},
		{Name: "c.timer", Service: "app", OnCalendar: []string{"daily"}},
	})
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	scheduler.mu.Lock()
	_, hasA := scheduler.timers["a.timer"]
	_, hasB := scheduler.timers["b.timer"]
	_, hasC := scheduler.timers["c.timer"]
	newA := scheduler.timers["a.timer"]
	scheduler.mu.Unlock()

	if !hasA || hasB || !hasC {
		t.Errorf("timers: a=%v b=%v c=%v", hasA, hasB, hasC)
	}
	if newA == oldA {
		t.Error("changed timer was not restarted")
	}
}
