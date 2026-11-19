package control

import (
	"fmt"
	"os"
	"path/filepath"
)

// ExecutablePath returns the path of the vsystemd executable next to the
// running binary.
func ExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	path := filepath.Join(filepath.Dir(executable), "vsystemd.exe")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("vsystemd executable not found at %s: %w", path, err)
	}
	return path, nil
}
