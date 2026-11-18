package storage

import (
	"path/filepath"
	"time"
)

const defaultRoot = `C:\ProgramData\vsystemd`

type TimerState struct {
	Enabled     bool      `json:"enabled"`
	LastRun     time.Time `json:"last_run"`
	NextRun     time.Time `json:"next_run"`
	ActiveSince time.Time `json:"active_since"`
}

func (s TimerState) UTC() TimerState {
	if !s.LastRun.IsZero() {
		s.LastRun = s.LastRun.UTC()
	}
	if !s.NextRun.IsZero() {
		s.NextRun = s.NextRun.UTC()
	}
	if !s.ActiveSince.IsZero() {
		s.ActiveSince = s.ActiveSince.UTC()
	}
	return s
}

type Storage interface {
	ListUnits() ([]string, error)
	ReadUnit(name string) ([]byte, error)
	ReadTimerState(name string) (TimerState, error)
	WriteTimerState(name string, state TimerState) error
	DeleteTimerState(name string) error
	ListTimerStates() (map[string]TimerState, error)
}

type Paths struct {
	SystemDir  string
	LogsDir    string
	TimersFile string
}

func DefaultPaths() Paths {
	return PathsFromRoot(defaultRoot)
}

func PathsFromRoot(root string) Paths {
	return Paths{
		SystemDir:  filepath.Join(root, "system"),
		LogsDir:    filepath.Join(root, "logs"),
		TimersFile: filepath.Join(root, "timers.json"),
	}
}
