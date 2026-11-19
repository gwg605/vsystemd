package unit

import (
	"fmt"
	"strings"
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

// ResolveOutputs parses StandardOutput/StandardError into output targets. The
// returned paths are raw (not cleaned or verified).
func (s *ServiceUnit) ResolveOutputs() (OutputTarget, OutputTarget, error) {
	stdout, err := resolveOutput(s.StandardOutput)
	if err != nil {
		return OutputTarget{}, OutputTarget{}, fmt.Errorf("StandardOutput: %w", err)
	}
	stderr, err := resolveOutput(s.StandardError)
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
	return OutputTarget{Kind: kind, Path: path}, nil
}

// StatusTarget returns the target to read a status log tail from: stdout unless
// it is discarded, in which case stderr.
func StatusTarget(stdout, stderr OutputTarget) OutputTarget {
	if stdout.Kind != OutputNull {
		return stdout
	}
	return stderr
}
