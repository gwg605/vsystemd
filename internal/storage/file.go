package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type fileStorage struct {
	paths Paths
	mu    sync.Mutex
}

func New(paths Paths) (Storage, error) {
	if paths.SystemDir == "" || paths.LogsDir == "" || paths.TimersFile == "" {
		return nil, errors.New("storage paths must not be empty")
	}
	for _, dir := range []string{paths.SystemDir, paths.LogsDir, filepath.Dir(paths.TimersFile)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return &fileStorage{paths: paths}, nil
}

func (s *fileStorage) ListUnits() ([]string, error) {
	entries, err := os.ReadDir(s.paths.SystemDir)
	if err != nil {
		return nil, fmt.Errorf("list units: %w", err)
	}
	var units []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".service", ".timer":
			units = append(units, entry.Name())
		}
	}
	return units, nil
}

func (s *fileStorage) ReadUnit(name string) ([]byte, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.paths.SystemDir, name))
	if err != nil {
		return nil, fmt.Errorf("read unit %s: %w", name, err)
	}
	return data, nil
}

func (s *fileStorage) ReadTimerState(name string) (TimerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	states, err := s.loadTimerStates()
	if err != nil {
		return TimerState{}, err
	}
	return states[name], nil
}

func (s *fileStorage) WriteTimerState(name string, state TimerState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	states, err := s.loadTimerStates()
	if err != nil {
		return err
	}
	states[name] = state.UTC()
	return s.saveTimerStates(states)
}

func (s *fileStorage) DeleteTimerState(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	states, err := s.loadTimerStates()
	if err != nil {
		return err
	}
	delete(states, name)
	return s.saveTimerStates(states)
}

func (s *fileStorage) ListTimerStates() (map[string]TimerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadTimerStates()
}

func (s *fileStorage) loadTimerStates() (map[string]TimerState, error) {
	states := make(map[string]TimerState)
	data, err := os.ReadFile(s.paths.TimersFile)
	if errors.Is(err, os.ErrNotExist) {
		return states, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read timer state: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return states, nil
	}
	if err := json.Unmarshal(data, &states); err != nil {
		return nil, fmt.Errorf("parse timer state: %w", err)
	}
	for name, state := range states {
		states[name] = state.UTC()
	}
	return states, nil
}

func (s *fileStorage) saveTimerStates(states map[string]TimerState) error {
	normalized := make(map[string]TimerState, len(states))
	for name, state := range states {
		normalized[name] = state.UTC()
	}
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return fmt.Errorf("encode timer state: %w", err)
	}
	if err := writeFileAtomic(s.paths.TimersFile, data, 0o644); err != nil {
		return fmt.Errorf("write timer state: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".timers-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func validateName(name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return fmt.Errorf("invalid name %q", name)
	}
	return nil
}
