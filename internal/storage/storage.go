package storage

import (
	"path/filepath"

	"github.com/gwg605/vsystemd/internal/unit"
)

const defaultRoot = `C:\ProgramData\vsystemd`

type Storage interface {
	ListUnits() ([]string, error)
	ReadUnit(name string) (unit.Content, error)
	// ReadTimers returns the raw contents of the timers file, or nil when it
	// does not exist. The layout of the data is owned by the caller.
	ReadTimers() ([]byte, error)
	// WriteTimers atomically replaces the timers file with data.
	WriteTimers(data []byte) error

	// ValidatePath rejects a path that is not a plain absolute file path.
	ValidatePath(path string) error
	// OutputWritable reports whether an output file's parent directory exists
	// and is writable.
	OutputWritable(path string) error
	// InputReadable reports whether an input file can be read.
	InputReadable(path string) error
}

// Factory opens a Storage.
type Factory func() (Storage, error)

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
