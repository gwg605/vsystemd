package fake

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/gwg605/vsystemd/internal/storage"
)

type Storage struct {
	mu     sync.Mutex
	units  map[string][]byte
	logs   map[string][]byte
	timers map[string]storage.TimerState
}

var _ storage.Storage = (*Storage)(nil)

func New() *Storage {
	return &Storage{
		units:  make(map[string][]byte),
		logs:   make(map[string][]byte),
		timers: make(map[string]storage.TimerState),
	}
}

func (s *Storage) WriteUnit(name string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.units[name] = append([]byte(nil), data...)
}

func (s *Storage) DeleteUnit(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.units, name)
}

func (s *Storage) ListUnits() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	units := make([]string, 0, len(s.units))
	for name := range s.units {
		switch strings.ToLower(filepath.Ext(name)) {
		case ".service", ".timer":
			units = append(units, name)
		}
	}
	sort.Strings(units)
	return units, nil
}

func (s *Storage) ReadUnit(name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.units[name]
	if !ok {
		return nil, fmt.Errorf("read unit %s: %w", name, os.ErrNotExist)
	}
	return append([]byte(nil), data...), nil
}

func (s *Storage) ReadTimerState(name string) (storage.TimerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timers[name].UTC(), nil
}

func (s *Storage) WriteTimerState(name string, state storage.TimerState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timers[name] = state.UTC()
	return nil
}

func (s *Storage) DeleteTimerState(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.timers, name)
	return nil
}

func (s *Storage) ListTimerStates() (map[string]storage.TimerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	states := make(map[string]storage.TimerState, len(s.timers))
	for name, state := range s.timers {
		states[name] = state.UTC()
	}
	return states, nil
}

func (s *Storage) ValidatePath(path string) error { return storage.ValidatePath(path) }

func (s *Storage) OutputWritable(path string) error { return storage.OutputWritable(path) }

func (s *Storage) InputReadable(path string) error { return storage.InputReadable(path) }
