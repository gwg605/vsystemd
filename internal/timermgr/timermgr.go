package timermgr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
	_ "time/tzdata"

	systemdtime "gitlab.com/allddd/go-systemd-time"

	"github.com/gwg605/vsystemd/internal/storage"
)

// ErrClosed reports that the manager is shutting down.
var ErrClosed = errors.New("timer manager closed")

// ErrNotFound reports that a timer does not exist.
var ErrNotFound = errors.New("timer not found")

// TimerConfig is the persisted configuration of a timer. The target service is
// resolved by the caller from the timer unit, so it is not part of the config.
type TimerConfig struct {
	Name       string
	OnCalendar []string
}

// TimerStatus is the runtime view of a timer. Enabled and Running are
// independent: Enabled is an administrative flag stored in the timer database,
// Running reports whether the timer is currently scheduled.
type TimerStatus struct {
	Name        string
	Enabled     bool
	Running     bool
	LastRun     time.Time
	NextRun     time.Time
	ActiveSince time.Time
}

// EventFunc is called when a timer fires.
type EventFunc func(name string, status TimerStatus)

// Manager owns the timer database and the in-process OnCalendar engine. It is
// independent of the SCM: when a timer fires it reports the event through
// EventFunc and the caller decides what to start.
type Manager struct {
	store   storage.Storage
	onEvent EventFunc

	mu      sync.Mutex
	timers  map[string]*timer
	started bool
	stopped bool
}

type timer struct {
	config      TimerConfig
	events      []systemdtime.CalendarEvent
	enabled     bool
	lastRun     time.Time
	nextRun     time.Time
	activeSince time.Time

	scheduled *scheduledTimer
}

type scheduledTimer struct {
	stop chan struct{}
	done chan struct{}
}

// persisted is the on-disk representation of a timer. Running is runtime-only
// and is re-derived on load (enabled timers are scheduled at startup).
type persisted struct {
	OnCalendar  []string  `json:"on_calendar"`
	Enabled     bool      `json:"enabled"`
	LastRun     time.Time `json:"last_run"`
	NextRun     time.Time `json:"next_run"`
	ActiveSince time.Time `json:"active_since"`
}

// New loads the timer database into memory. It does not schedule anything and
// never invokes the event handler; call Start to run the scheduler.
func New(store storage.Storage, onEvent EventFunc) (*Manager, error) {
	m := &Manager{store: store, onEvent: onEvent, timers: make(map[string]*timer)}

	data, err := store.ReadTimers()
	if err != nil {
		return nil, fmt.Errorf("read timers: %w", err)
	}
	if len(bytes.TrimSpace(data)) > 0 {
		var raw map[string]persisted
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse timers: %w", err)
		}
		for name, p := range raw {
			events, err := parseEvents(name, p.OnCalendar)
			if err != nil {
				slog.Warn("timermgr: skip timer", "unit", name, "error", err)
				continue
			}
			m.timers[name] = &timer{
				config:      TimerConfig{Name: name, OnCalendar: p.OnCalendar},
				events:      events,
				enabled:     p.Enabled,
				lastRun:     utcTime(p.LastRun),
				nextRun:     utcTime(p.NextRun),
				activeSince: utcTime(p.ActiveSince),
			}
		}
	}

	return m, nil
}

// Start runs the scheduler, scheduling every enabled timer. It is idempotent.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return ErrClosed
	}
	if m.started {
		return nil
	}
	m.started = true
	for _, t := range m.timers {
		if t.enabled {
			m.schedule(t)
		}
	}
	return nil
}

// SetTimer creates or updates a timer. It never changes the enabled or running
// state; a running timer is rescheduled only when its OnCalendar changes.
func (m *Manager) SetTimer(cfg TimerConfig) error {
	if cfg.Name == "" {
		return errors.New("timer name is required")
	}
	if len(cfg.OnCalendar) == 0 {
		return fmt.Errorf("timer %s: OnCalendar is required", cfg.Name)
	}
	events, err := parseEvents(cfg.Name, cfg.OnCalendar)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return ErrClosed
	}
	t := m.timers[cfg.Name]
	if t == nil {
		t = &timer{}
		m.timers[cfg.Name] = t
	}
	changed := !slices.Equal(t.config.OnCalendar, cfg.OnCalendar)
	t.config = cfg
	t.events = events

	var old *scheduledTimer
	if changed && t.scheduled != nil {
		old = m.unschedule(t)
		t.activeSince = time.Time{}
		t.nextRun = time.Time{}
		m.schedule(t)
	}
	err = m.saveLocked()
	m.mu.Unlock()

	waitDone(old)
	return err
}

// DeleteTimer removes a timer. A timer that is enabled or running must be
// stopped and disabled first.
func (m *Manager) DeleteTimer(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return ErrClosed
	}
	t := m.timers[name]
	if t == nil {
		return ErrNotFound
	}
	if t.enabled || t.scheduled != nil {
		return fmt.Errorf("timer %s is enabled or running", name)
	}
	delete(m.timers, name)
	return m.saveLocked()
}

// Enable sets the administrative enabled flag without starting or stopping the
// timer.
func (m *Manager) Enable(name string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return ErrClosed
	}
	t := m.timers[name]
	if t == nil {
		return ErrNotFound
	}
	t.enabled = enabled
	return m.saveLocked()
}

// StartTimer schedules the timer. It is a no-op when already running.
func (m *Manager) StartTimer(name string) error {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return ErrClosed
	}
	t := m.timers[name]
	if t == nil {
		m.mu.Unlock()
		return ErrNotFound
	}
	if t.scheduled == nil {
		if _, ok := nextEvent(t.events, time.Now()); !ok {
			m.mu.Unlock()
			return fmt.Errorf("timer %s has no future events", name)
		}
		m.schedule(t)
		err := m.saveLocked()
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	return nil
}

// StopTimer removes the timer from the schedule, keeping its last run time.
func (m *Manager) StopTimer(name string) error {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return ErrClosed
	}
	t := m.timers[name]
	if t == nil {
		m.mu.Unlock()
		return ErrNotFound
	}
	old := m.unschedule(t)
	t.nextRun = time.Time{}
	t.activeSince = time.Time{}
	err := m.saveLocked()
	m.mu.Unlock()

	waitDone(old)
	return err
}

// QueryStatus returns the runtime status of a single timer.
func (m *Manager) QueryStatus(name string) (TimerStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.timers[name]
	if t == nil {
		return TimerStatus{}, ErrNotFound
	}
	return t.status(), nil
}

// QueryStatuses returns the runtime status of every timer.
func (m *Manager) QueryStatuses() ([]TimerStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	statuses := make([]TimerStatus, 0, len(m.timers))
	for _, t := range m.timers {
		statuses = append(statuses, t.status())
	}
	return statuses, nil
}

// ListTimers returns the configuration of every timer.
func (m *Manager) ListTimers() ([]TimerConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	configs := make([]TimerConfig, 0, len(m.timers))
	for _, t := range m.timers {
		configs = append(configs, t.config)
	}
	return configs, nil
}

// GetTimer returns the configuration of a single timer.
func (m *Manager) GetTimer(name string) (TimerConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.timers[name]
	if t == nil {
		return TimerConfig{}, ErrNotFound
	}
	return t.config, nil
}

// Stop stops every scheduled timer and shuts the manager down. It is
// idempotent; after Stop the manager rejects further operations.
func (m *Manager) Stop() error {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return nil
	}
	m.stopped = true
	var schedules []*scheduledTimer
	for _, t := range m.timers {
		if s := m.unschedule(t); s != nil {
			schedules = append(schedules, s)
		}
	}
	m.mu.Unlock()

	for _, s := range schedules {
		close(s.stop)
		<-s.done
	}
	return nil
}

// schedule starts the scheduling goroutine. It must be called with m.mu held.
func (m *Manager) schedule(t *timer) {
	next, ok := nextEvent(t.events, time.Now())
	if !ok {
		t.activeSince = time.Time{}
		t.nextRun = time.Time{}
		return
	}
	s := &scheduledTimer{stop: make(chan struct{}), done: make(chan struct{})}
	t.scheduled = s
	t.activeSince = time.Now().UTC()
	t.nextRun = next
	go m.run(t, s)
}

// unschedule detaches the scheduling goroutine without waiting for it. It must
// be called with m.mu held; the caller waits via waitDone after unlocking.
func (m *Manager) unschedule(t *timer) *scheduledTimer {
	s := t.scheduled
	t.scheduled = nil
	return s
}

func waitDone(s *scheduledTimer) {
	if s == nil {
		return
	}
	close(s.stop)
	<-s.done
}

func (m *Manager) run(t *timer, s *scheduledTimer) {
	defer close(s.done)

	for {
		m.mu.Lock()
		if m.stopped || t.scheduled != s {
			m.mu.Unlock()
			return
		}
		next, ok := nextEvent(t.events, time.Now())
		if !ok {
			t.scheduled = nil
			t.activeSince = time.Time{}
			t.nextRun = time.Time{}
			_ = m.saveLocked()
			m.mu.Unlock()
			return
		}
		t.nextRun = next
		_ = m.saveLocked()
		events := t.events
		name := t.config.Name
		m.mu.Unlock()

		wait := time.NewTimer(time.Until(next))
		select {
		case <-wait.C:
		case <-s.stop:
			wait.Stop()
			return
		}

		firedAt := time.Now().UTC()
		next, ok = nextEvent(events, firedAt.Local())

		m.mu.Lock()
		if m.stopped || t.scheduled != s {
			m.mu.Unlock()
			return
		}
		t.lastRun = firedAt
		if ok {
			t.nextRun = next
		} else {
			t.nextRun = time.Time{}
		}
		status := t.status()
		_ = m.saveLocked()
		m.mu.Unlock()

		if m.onEvent != nil {
			m.onEvent(name, status)
		}

		if !ok {
			m.mu.Lock()
			if t.scheduled == s {
				t.scheduled = nil
				t.activeSince = time.Time{}
				_ = m.saveLocked()
			}
			m.mu.Unlock()
			return
		}
	}
}

func (t *timer) status() TimerStatus {
	return TimerStatus{
		Name:        t.config.Name,
		Enabled:     t.enabled,
		Running:     t.scheduled != nil,
		LastRun:     t.lastRun,
		NextRun:     t.nextRun,
		ActiveSince: t.activeSince,
	}
}

// saveLocked persists the timer database. It must be called with m.mu held.
func (m *Manager) saveLocked() error {
	data := make(map[string]persisted, len(m.timers))
	for name, t := range m.timers {
		data[name] = persisted{
			OnCalendar:  t.config.OnCalendar,
			Enabled:     t.enabled,
			LastRun:     utcTime(t.lastRun),
			NextRun:     utcTime(t.nextRun),
			ActiveSince: utcTime(t.activeSince),
		}
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		slog.Error("timermgr: encode timers failed", "error", err)
		return fmt.Errorf("encode timers: %w", err)
	}
	if err := m.store.WriteTimers(encoded); err != nil {
		slog.Warn("timermgr: write timers failed", "error", err)
		return fmt.Errorf("write timers: %w", err)
	}
	return nil
}

func parseEvents(name string, onCalendar []string) ([]systemdtime.CalendarEvent, error) {
	events := make([]systemdtime.CalendarEvent, 0, len(onCalendar))
	for _, expression := range onCalendar {
		event, err := systemdtime.ParseCalendarEvent(expression)
		if err != nil {
			return nil, fmt.Errorf("timer %s: parse OnCalendar %q: %w", name, expression, err)
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
