package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gwg605/vsystemd/internal/storage"
)

// resolveLogsDirectories turns LogsDirectory= names into absolute paths below
// base. Names must be single relative components without traversal.
func resolveLogsDirectories(store storage.Storage, base string, names []string) ([]string, error) {
	var dirs []string
	for _, name := range names {
		if err := validateDirectoryName(name); err != nil {
			return nil, err
		}
		dir := filepath.Join(base, name)
		if err := store.ValidatePath(dir); err != nil {
			return nil, err
		}
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

func validateDirectoryName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("empty LogsDirectory name")
	case filepath.IsAbs(name):
		return fmt.Errorf("LogsDirectory %q must be relative", name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("LogsDirectory %q must not contain a path separator", name)
	case name == "." || name == "..":
		return fmt.Errorf("LogsDirectory %q is not allowed", name)
	default:
		return nil
	}
}

func ensureDirectories(dirs []string) error {
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create logs directory %s: %w", dir, err)
		}
	}
	return nil
}
