package storage

import (
	"path/filepath"
	"time"
)

const defaultRoot = `C:\ProgramData\vsystemd`

type TimerState struct {
	Enabled bool      `json:"enabled"`
	LastRun time.Time `json:"last_run"`
	NextRun time.Time `json:"next_run"`
}

type Storage interface {
	ListUnits() ([]string, error)
	ReadUnit(name string) ([]byte, error)
	WriteLog(name, stream string, data []byte) error
	ReadLogTail(name string, n int) ([]string, error)
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
