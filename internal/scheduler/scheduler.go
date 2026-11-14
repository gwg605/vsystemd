package scheduler

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
	_ "time/tzdata"

	systemdtime "gitlab.com/allddd/go-systemd-time"

	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/storage"
)

var ErrClosed = errors.New("scheduler closed")

type Timer struct {
	Name       string
	Service    string
	OnCalendar []string
}

type Scheduler struct {
	manager scm.Manager
	store   storage.Storage

	mu     sync.Mutex
	timers map[string]*scheduledTimer
	closed bool
}

type scheduledTimer struct {
	timer  Timer
	events []systemdtime.CalendarEvent
	stop   chan struct{}
	done   chan struct{}
}

func New(manager scm.Manager, store storage.Storage) *Scheduler {
	return &Scheduler{
		manager: manager,
		store:   store,
		timers:  make(map[string]*scheduledTimer),
	}
}

func (s *Scheduler) Add(timer Timer) error {
	events, err := parseEvents(timer)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if _, ok := s.timers[timer.Name]; ok {
		return fmt.Errorf("timer %s is already scheduled", timer.Name)
	}

	scheduled := &scheduledTimer{
		timer:  timer,
		events: events,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	s.timers[timer.Name] = scheduled
	if next, ok := nextEvent(events, time.Now()); ok {
		s.writeNext(timer.Name, next)
	}
	slog.Info("scheduler: timer added", "unit", timer.Name, "service", timer.Service, "on_calendar", timer.OnCalendar)
	go s.run(scheduled)
	return nil
}

func (s *Scheduler) Remove(name string) error {
	slog.Info("scheduler: timer removed", "unit", name)
	if !s.stop(name) {
		return nil
	}
	return s.store.DeleteTimerState(name)
}

func (s *Scheduler) Stop(name string) error {
	slog.Info("scheduler: timer stopped", "unit", name)
	s.stop(name)
	return s.clearNext(name)
}

func (s *Scheduler) clearNext(name string) error {
	state, err := s.store.ReadTimerState(name)
	if err != nil {
		slog.Warn("scheduler: read timer state failed", "unit", name, "error", err)
		return err
	}
	if state.NextRun.IsZero() {
		return nil
	}
	state.NextRun = time.Time{}
	if err := s.store.WriteTimerState(name, state); err != nil {
		slog.Warn("scheduler: write timer state failed", "unit", name, "error", err)
		return err
	}
	return nil
}

func (s *Scheduler) Has(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.timers[name]
	return ok
}

func (s *Scheduler) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.timers))
	for name := range s.timers {
		names = append(names, name)
	}
	return names
}

func (s *Scheduler) Reload(timers []Timer) error {
	slog.Info("scheduler: reload", "timer_count", len(timers))
	desired := make(map[string]Timer, len(timers))
	for _, timer := range timers {
		if _, err := parseEvents(timer); err != nil {
			return err
		}
		desired[timer.Name] = timer
	}

	s.mu.Lock()
	existing := make(map[string]Timer, len(s.timers))
	for name, scheduled := range s.timers {
		existing[name] = scheduled.timer
	}
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return ErrClosed
	}

	for name, old := range existing {
		newTimer, ok := desired[name]
		if !ok {
			slog.Info("scheduler: removing timer on reload", "unit", name)
			s.stop(name)
			_ = s.store.DeleteTimerState(name)
			continue
		}
		if changed(old, newTimer) {
			slog.Info("scheduler: restarting changed timer", "unit", name)
			s.stop(name)
		}
	}

	for name, timer := range desired {
		old, ok := existing[name]
		if ok && !changed(old, timer) {
			continue
		}
		if err := s.Add(timer); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	timers := make([]*scheduledTimer, 0, len(s.timers))
	for name, timer := range s.timers {
		timers = append(timers, timer)
		delete(s.timers, name)
	}
	for _, timer := range timers {
		close(timer.stop)
	}
	s.mu.Unlock()

	for _, timer := range timers {
		<-timer.done
	}
	return nil
}

func (s *Scheduler) stop(name string) bool {
	s.mu.Lock()
	timer := s.timers[name]
	if timer == nil {
		s.mu.Unlock()
		return false
	}
	delete(s.timers, name)
	close(timer.stop)
	s.mu.Unlock()

	<-timer.done
	return true
}

func (s *Scheduler) run(timer *scheduledTimer) {
	defer close(timer.done)

	for {
		next, ok := nextEvent(timer.events, time.Now())
		if !ok {
			slog.Warn("scheduler: no future timer events, stopping timer", "unit", timer.timer.Name)
			return
		}
		s.writeNext(timer.timer.Name, next)
		slog.Debug("scheduler: waiting for next timer event", "unit", timer.timer.Name, "next_run", next)

		wait := time.NewTimer(time.Until(next))
		select {
		case <-wait.C:
		case <-timer.stop:
			wait.Stop()
			return
		}

		firedAt := time.Now().UTC()
		slog.Info("scheduler: firing timer", "unit", timer.timer.Name, "service", timer.timer.Service, "fired_at", firedAt)
		if err := s.manager.StartService(timer.timer.Service); err != nil {
			slog.Error("scheduler: start service failed", "unit", timer.timer.Name, "service", timer.timer.Service, "error", err)
		}
		next, ok = nextEvent(timer.events, firedAt.Local())
		s.writeTick(timer.timer.Name, firedAt, next)
		if !ok {
			slog.Warn("scheduler: no future timer events, stopping timer", "unit", timer.timer.Name)
			return
		}
	}
}

func (s *Scheduler) writeNext(name string, next time.Time) {
	state, err := s.store.ReadTimerState(name)
	if err != nil {
		slog.Warn("scheduler: read timer state failed", "unit", name, "error", err)
		return
	}
	state.NextRun = utcTime(next)
	if err := s.store.WriteTimerState(name, state); err != nil {
		slog.Warn("scheduler: write timer state failed", "unit", name, "error", err)
	}
}

func (s *Scheduler) writeTick(name string, firedAt, next time.Time) {
	state, err := s.store.ReadTimerState(name)
	if err != nil {
		slog.Warn("scheduler: read timer state failed", "unit", name, "error", err)
		return
	}
	state.LastRun = utcTime(firedAt)
	state.NextRun = utcTime(next)
	if err := s.store.WriteTimerState(name, state); err != nil {
		slog.Warn("scheduler: write timer state failed", "unit", name, "error", err)
	}
}

func parseEvents(timer Timer) ([]systemdtime.CalendarEvent, error) {
	if timer.Name == "" {
		return nil, errors.New("timer name is required")
	}
	if timer.Service == "" {
		return nil, fmt.Errorf("timer %s: service is required", timer.Name)
	}
	if len(timer.OnCalendar) == 0 {
		return nil, fmt.Errorf("timer %s: OnCalendar is required", timer.Name)
	}

	events := make([]systemdtime.CalendarEvent, 0, len(timer.OnCalendar))
	for _, expression := range timer.OnCalendar {
		event, err := systemdtime.ParseCalendarEvent(expression)
		if err != nil {
			return nil, fmt.Errorf("timer %s: parse OnCalendar %q: %w", timer.Name, expression, err)
		}
		events = append(events, event)
	}
	return events, nil
}

func nextEvent(events []systemdtime.CalendarEvent, now time.Time) (time.Time, bool) {
	var next time.Time
	for _, event := range events {
		candidate, ok := event.Next(now)
		if !ok {
			continue
		}
		candidate = candidate.UTC()
		if next.IsZero() || candidate.Before(next) {
			next = candidate
		}
	}
	return next, !next.IsZero()
}

func utcTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC()
}

func changed(old, next Timer) bool {
	return old.Service != next.Service || !slices.Equal(old.OnCalendar, next.OnCalendar)
}
