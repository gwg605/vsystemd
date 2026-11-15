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
	return OutputTarget{Kind: kind, Path: filepath.Clean(path)}, nil
}

// ValidateUnitPath rejects non-absolute paths and paths containing a ".."
// component.
func ValidateUnitPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path %q must be absolute", path)
	}
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '\\' || r == '/' }) {
		if part == ".." {
			return fmt.Errorf("path %q must not contain \"..\"", path)
		}
	}
	return nil
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
