package scheduler

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

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
	go s.run(scheduled)
	return nil
}

func (s *Scheduler) Remove(name string) error {
	if !s.stop(name) {
		return nil
	}
	return s.store.DeleteTimerState(name)
}

func (s *Scheduler) Reload(timers []Timer) error {
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
			s.stop(name)
			_ = s.store.DeleteTimerState(name)
			continue
		}
		if changed(old, newTimer) {
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
			return
		}
		s.writeNext(timer.timer.Name, next)

		wait := time.NewTimer(time.Until(next))
		select {
		case <-wait.C:
		case <-timer.stop:
			wait.Stop()
			return
		}

		firedAt := time.Now()
		_ = s.manager.StartService(timer.timer.Service)
		next, ok = nextEvent(timer.events, firedAt)
		s.writeTick(timer.timer.Name, firedAt, next)
		if !ok {
			return
		}
	}
}

func (s *Scheduler) writeNext(name string, next time.Time) {
	state, err := s.store.ReadTimerState(name)
	if err != nil {
		return
	}
	state.NextRun = next
	_ = s.store.WriteTimerState(name, state)
}

func (s *Scheduler) writeTick(name string, firedAt, next time.Time) {
	state, err := s.store.ReadTimerState(name)
	if err != nil {
		return
	}
	state.LastRun = firedAt
	state.NextRun = next
	_ = s.store.WriteTimerState(name, state)
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
		if next.IsZero() || candidate.Before(next) {
			next = candidate
		}
	}
	return next, !next.IsZero()
}

func changed(old, next Timer) bool {
	return old.Service != next.Service || !slices.Equal(old.OnCalendar, next.OnCalendar)
}
