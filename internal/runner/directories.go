package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gwg605/vsystemd/internal/storage"
)

// resolveDirectories turns directory names of a directive (LogsDirectory=,
// RuntimeDirectory=, ...) into absolute paths below base. Names must be single
// relative components without traversal.
func resolveDirectories(store storage.Storage, base string, names []string, directive string) ([]string, error) {
	var dirs []string
	for _, name := range names {
		if err := validateDirectoryName(name, directive); err != nil {
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

func validateDirectoryName(name, directive string) error {
	switch {
	case name == "":
		return fmt.Errorf("empty %s name", directive)
	case filepath.IsAbs(name):
		return fmt.Errorf("%s %q must be relative", directive, name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("%s %q must not contain a path separator", directive, name)
	case name == "." || name == "..":
		return fmt.Errorf("%s %q is not allowed", directive, name)
	default:
		return nil
	}
}

func ensureDirectories(dirs []string) error {
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

// resetDirectories removes and recreates dirs, so a fresh service start does
// not reuse leftovers from a previous run.
func resetDirectories(dirs []string) error {
	if err := removeDirectories(dirs); err != nil {
		return err
	}
	return ensureDirectories(dirs)
}

func removeDirectories(dirs []string) error {
	for _, dir := range dirs {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove directory %s: %w", dir, err)
		}
	}
	return nil
}
