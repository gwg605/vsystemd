package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidatePath rejects anything that is not a plain absolute file path: a
// non-absolute path, ".."/"." components, components with trailing dots or
// spaces (which Windows strips), alternate data streams, device paths
// ("\\.\...", "\\?\...") and reserved device names.
func ValidatePath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path %q must be absolute", path)
	}
	if strings.HasPrefix(path, `\\.\`) || strings.HasPrefix(path, `\\?\`) {
		return fmt.Errorf("path %q must not be a device path", path)
	}
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '\\' || r == '/' })
	for index, part := range parts {
		if part == ".." || part == "." {
			return fmt.Errorf("path %q must not contain %q", path, part)
		}
		if strings.TrimRight(part, ". ") != part {
			return fmt.Errorf("path %q contains a component with a trailing dot or space", path)
		}
		if strings.ContainsRune(part, ':') && !(index == 0 && isDriveComponent(part)) {
			return fmt.Errorf("path %q contains a colon in a component", path)
		}
		if isReservedName(part) {
			return fmt.Errorf("path %q uses the reserved name %q", path, part)
		}
	}
	return nil
}

// OutputWritable reports whether the parent directory of a custom output path
// exists and is writable. It does not create or modify the target file.
func OutputWritable(path string) error {
	dir := filepath.Dir(filepath.Clean(path))
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	probe, err := os.CreateTemp(dir, ".vsystemd-writecheck-*")
	if err != nil {
		return fmt.Errorf("directory %s is not writable: %w", dir, err)
	}
	name := probe.Name()
	probe.Close()
	_ = os.Remove(name)
	return nil
}

// InputReadable reports whether an input file (EnvironmentFile=) can be read.
func InputReadable(path string) error {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	return file.Close()
}

func isDriveComponent(part string) bool {
	if len(part) != 2 || part[1] != ':' {
		return false
	}
	c := part[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isReservedName(part string) bool {
	base := part
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch strings.ToUpper(base) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}
