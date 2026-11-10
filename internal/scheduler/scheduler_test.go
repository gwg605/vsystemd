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
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timer state was not persisted")
		}
		time.Sleep(20 * time.Millisecond)
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

	state, err := store.ReadTimerState("b.timer")
	if err != nil {
		t.Fatalf("ReadTimerState() error = %v", err)
	}
	if !state.LastRun.IsZero() || !state.NextRun.IsZero() || state.Enabled {
		t.Errorf("removed timer state = %+v", state)
	}
}
