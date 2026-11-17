package logwriter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gwg605/vsystemd/internal/unit"
)

type OutputKind int

const (
	OutputDefault OutputKind = iota
	OutputNull
	OutputFile
	OutputAppend
)

type OutputTarget struct {
	Kind OutputKind
	Path string
}

// ResolveServiceOutputs resolves StandardOutput/StandardError into output
// targets. Unsupported values and invalid custom paths are errors.
func ResolveServiceOutputs(service *unit.ServiceUnit) (OutputTarget, OutputTarget, error) {
	stdout, err := resolveOutput(service.StandardOutput)
	if err != nil {
		return OutputTarget{}, OutputTarget{}, fmt.Errorf("StandardOutput: %w", err)
	}
	stderr, err := resolveOutput(service.StandardError)
	if err != nil {
		return OutputTarget{}, OutputTarget{}, fmt.Errorf("StandardError: %w", err)
	}
	return stdout, stderr, nil
}

func resolveOutput(value string) (OutputTarget, error) {
	switch {
	case value == "":
		return OutputTarget{Kind: OutputDefault}, nil
	case value == "null":
		return OutputTarget{Kind: OutputNull}, nil
	case strings.HasPrefix(value, "file:"):
		return explicitTarget(OutputFile, strings.TrimPrefix(value, "file:"))
	case strings.HasPrefix(value, "append:"):
		return explicitTarget(OutputAppend, strings.TrimPrefix(value, "append:"))
	default:
		return OutputTarget{}, fmt.Errorf("unsupported value %q", value)
	}
}

func explicitTarget(kind OutputKind, path string) (OutputTarget, error) {
	if path == "" {
		return OutputTarget{}, fmt.Errorf("empty file path")
	}
	if err := ValidateUnitPath(path); err != nil {
		return OutputTarget{}, err
	}
	cleaned := filepath.Clean(path)
	if err := checkOutputDir(cleaned); err != nil {
		return OutputTarget{}, err
	}
	if info, err := os.Stat(cleaned); err == nil && info.IsDir() {
		return OutputTarget{}, fmt.Errorf("%s is a directory", cleaned)
	}
	return OutputTarget{Kind: kind, Path: cleaned}, nil
}

// checkOutputDir verifies that the directory containing path exists and is a
// directory.
func checkOutputDir(path string) error {
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("output directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return nil
}

// ValidateUnitPath rejects anything that is not a plain absolute file path: a
// non-absolute path, ".."/"." components, components with trailing dots or
// spaces (which Windows strips), alternate data streams, device paths
// ("\\.\...", "\\?\...") and reserved device names.
func ValidateUnitPath(path string) error {
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

// CheckOutputWritable reports whether the parent directory of a custom output
// path exists and is writable. It does not create or modify the target file.
func CheckOutputWritable(path string) error {
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

// CheckEnvReadable reports whether an environment file can be read.
func CheckEnvReadable(path string) error {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	return file.Close()
}

func SamePath(left, right string) bool {
	return CanonicalPath(left) == CanonicalPath(right)
}

func CanonicalPath(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

func StatusTarget(stdout, stderr OutputTarget) OutputTarget {
	if stdout.Kind != OutputNull {
		return stdout
	}
	return stderr
}

func ServiceLogDir(logsDir, serviceName string) string {
	return filepath.Join(logsDir, serviceName)
}
