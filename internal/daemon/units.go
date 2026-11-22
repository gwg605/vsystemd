package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/timermgr"
	"github.com/gwg605/vsystemd/internal/unit"
)

// LoadState is the result of loading a unit file.
type LoadState string

const (
	LoadStateLoaded     LoadState = "loaded"
	LoadStateBadSetting LoadState = "bad-setting"
	LoadStateError      LoadState = "error"
	LoadStateNotFound   LoadState = "not-found"
)

var errConfigurationMissing = errors.New("configuration file is missing")

type loadError struct {
	state LoadState
	err   error
}

func (e *loadError) Error() string { return e.err.Error() }

func (e *loadError) Unwrap() error { return e.err }

func classifyLoadError(err error) LoadState {
	if errors.Is(err, os.ErrNotExist) {
		return LoadStateNotFound
	}
	return LoadStateError
}

// unitEntry is the daemon's view of a unit: the last successfully parsed
// configuration (the target) plus the current file health.
type unitEntry struct {
	id          unit.ID
	parsed      *unit.UnitFile
	loadState   LoadState
	loadError   string
	hasFile     bool
	contentHash string
}

// UnitsList is the daemon's source of truth for unit configuration. It is
// updated only on daemon-reload (and built at startup), never by reads.
type UnitsList struct {
	store   storage.Storage
	manager scm.Manager
	timers  *timermgr.Manager

	mu    sync.RWMutex
	units map[string]unitEntry
}

func newUnitsList(store storage.Storage, manager scm.Manager, timers *timermgr.Manager) *UnitsList {
	return &UnitsList{store: store, manager: manager, timers: timers, units: make(map[string]unitEntry)}
}

func (l *UnitsList) entry(id unit.ID) (unitEntry, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	e, ok := l.units[id.String()]
	return e, ok
}

func (l *UnitsList) all() []unitEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	entries := make([]unitEntry, 0, len(l.units))
	for _, e := range l.units {
		entries = append(entries, e)
	}
	return entries
}

func (l *UnitsList) keys() map[string]bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	keys := make(map[string]bool, len(l.units))
	for name := range l.units {
		keys[name] = true
	}
	return keys
}

// rebuild updates the list in place from the current files, SCM services and
// enabled timers. It returns an error on an infrastructure failure without
// touching the current list.
func (l *UnitsList) rebuild() error {
	next := l.copy()
	for name, entry := range next {
		entry.hasFile = false
		next[name] = entry
	}
	present := make(map[string]bool)

	if err := l.mergeFiles(next, present); err != nil {
		return err
	}
	if err := l.mergeServices(next, present); err != nil {
		return err
	}
	if err := l.mergeTimers(next, present); err != nil {
		return err
	}
	l.validateTimers(next)

	for name := range next {
		if !present[name] {
			delete(next, name)
		}
	}

	l.mu.Lock()
	l.units = next
	l.mu.Unlock()
	return nil
}

func (l *UnitsList) copy() map[string]unitEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	next := make(map[string]unitEntry, len(l.units))
	for name, entry := range l.units {
		next[name] = entry
	}
	return next
}

func (l *UnitsList) mergeFiles(next map[string]unitEntry, present map[string]bool) error {
	names, err := l.store.ListUnits()
	if err != nil {
		return fmt.Errorf("list units: %w", err)
	}
	for _, name := range names {
		id, err := unit.ParseID(name)
		if err != nil {
			slog.Warn("units: skip unit", "unit", name, "error", err)
			continue
		}
		if id.Name == config.ManagerServiceName {
			continue
		}
		present[id.String()] = true

		entry := next[id.String()]
		entry.id = id
		entry.hasFile = true

		data, readErr := l.store.ReadUnit(id.String())
		if readErr != nil {
			entry.loadState = classifyLoadError(readErr)
			entry.loadError = readErr.Error()
			next[id.String()] = entry
			slog.Warn("units: read unit failed", "unit", id, "error", readErr)
			continue
		}
		parsed, parseErr := unit.Parse(id, data)
		if parseErr != nil {
			entry.loadState = LoadStateBadSetting
			entry.loadError = parseErr.Error()
			next[id.String()] = entry
			slog.Warn("units: invalid unit", "unit", id, "error", parseErr)
			continue
		}
		for _, warning := range parsed.Warnings {
			slog.Warn("units: ignored unit directive", "unit", id, "warning", warning)
		}
		entry.parsed = parsed
		entry.contentHash = data.Hash()
		entry.loadState = LoadStateLoaded
		entry.loadError = ""
		next[id.String()] = entry
	}
	return nil
}

func (l *UnitsList) mergeServices(next map[string]unitEntry, present map[string]bool) error {
	configs, err := l.manager.ListServices()
	if err != nil {
		return fmt.Errorf("list services: %w", err)
	}
	for _, cfg := range configs {
		if cfg.Name == config.ManagerServiceName {
			continue
		}
		id := unit.ID{Name: cfg.Name, Type: unit.TypeService}
		present[id.String()] = true

		entry := next[id.String()]
		entry.id = id
		if !entry.hasFile {
			entry.loadState = LoadStateNotFound
			entry.loadError = errConfigurationMissing.Error()
		}
		next[id.String()] = entry
	}
	return nil
}

func (l *UnitsList) mergeTimers(next map[string]unitEntry, present map[string]bool) error {
	configs, err := l.timers.ListTimers()
	if err != nil {
		return fmt.Errorf("list timers: %w", err)
	}
	for _, cfg := range configs {
		id, err := unit.ParseID(cfg.Name)
		if err != nil || id.Type != unit.TypeTimer {
			continue
		}
		present[id.String()] = true

		entry := next[id.String()]
		entry.id = id
		if !entry.hasFile {
			entry.loadState = LoadStateNotFound
			entry.loadError = errConfigurationMissing.Error()
		}
		next[id.String()] = entry
	}
	return nil
}

func (l *UnitsList) validateTimers(next map[string]unitEntry) {
	for name, entry := range next {
		if entry.id.Type != unit.TypeTimer || entry.loadState != LoadStateLoaded || entry.parsed == nil {
			continue
		}
		if err := timerSemanticError(entry); err != nil {
			entry.loadState = LoadStateBadSetting
			entry.loadError = err.Error()
			next[name] = entry
		}
	}
}

func timerSemanticError(entry unitEntry) error {
	if len(entry.parsed.Timer.OnCalendar) == 0 {
		return fmt.Errorf("timer %s has no OnCalendar", entry.id)
	}
	target, err := timerTarget(entry.parsed.Timer, entry.id)
	if err != nil {
		return err
	}
	if target.Name == config.ManagerServiceName {
		return fmt.Errorf("timer %s targets the vsystemd manager service", entry.id)
	}
	return nil
}
