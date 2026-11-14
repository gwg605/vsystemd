package logwriter

import (
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

func ResolveServiceOutputs(service *unit.ServiceUnit) (OutputTarget, OutputTarget, []string) {
	stdout, stdoutWarning := resolveOutput(service.StandardOutput, service.WorkingDirectory)
	stderr, stderrWarning := resolveOutput(service.StandardError, service.WorkingDirectory)

	var warnings []string
	if stdoutWarning != "" {
		warnings = append(warnings, "StandardOutput: "+stdoutWarning)
	}
	if stderrWarning != "" {
		warnings = append(warnings, "StandardError: "+stderrWarning)
	}
	return stdout, stderr, warnings
}

func resolveOutput(value, workingDirectory string) (OutputTarget, string) {
	switch {
	case value == "":
		return OutputTarget{Kind: OutputDefault}, ""
	case value == "null":
		return OutputTarget{Kind: OutputNull}, ""
	case strings.HasPrefix(value, "file:"):
		return explicitTarget(OutputFile, strings.TrimPrefix(value, "file:"), workingDirectory)
	case strings.HasPrefix(value, "append:"):
		return explicitTarget(OutputAppend, strings.TrimPrefix(value, "append:"), workingDirectory)
	default:
		return OutputTarget{Kind: OutputDefault}, "unsupported value " + value
	}
}

func explicitTarget(kind OutputKind, path, workingDirectory string) (OutputTarget, string) {
	if path == "" {
		return OutputTarget{Kind: OutputDefault}, "empty file path"
	}
	if !filepath.IsAbs(path) && workingDirectory != "" {
		path = filepath.Join(workingDirectory, path)
	}
	return OutputTarget{Kind: kind, Path: filepath.Clean(path)}, ""
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
