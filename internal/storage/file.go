package storage

import (
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
	data, err := os.ReadFile(filepath.Join(s.paths.SystemDir, name))
	if err != nil {
		return nil, fmt.Errorf("read unit %s: %w", name, err)
	}
	return data, nil
}

func (s *fileStorage) ReadTimers() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.paths.TimersFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read timers: %w", err)
	}
	return data, nil
}

func (s *fileStorage) WriteTimers(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeFileAtomic(s.paths.TimersFile, data, 0o644); err != nil {
		return fmt.Errorf("write timers: %w", err)
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

func (s *fileStorage) ValidatePath(path string) error { return ValidatePath(path) }

func (s *fileStorage) OutputWritable(path string) error { return OutputWritable(path) }

func (s *fileStorage) InputReadable(path string) error { return InputReadable(path) }

