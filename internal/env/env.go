package env

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/gwg605/vsystemd/internal/storage"
)

// Defaults describes how the clean environment for a service process is built:
// Inherit lists variables taken from the parent environment, Vars are extra
// variables whose values may reference the parent environment via %NAME%.
type Defaults struct {
	Inherit []string
	Vars    map[string]string
}

// Build returns the environment for a service process. unitEnv is the unit's
// Environment= assignments and files its EnvironmentFile= entries (a "-" prefix
// marks an optional file). Variables are deduplicated case-insensitively: a name
// keeps its first-seen spelling and its value comes from the last source.
func Build(unitEnv map[string]string, files []string, defaults Defaults) ([]string, error) {
	parentEnv := parent()

	env := make(map[string]string)
	names := make(map[string]string)
	set := func(name, value string) {
		lower := strings.ToLower(name)
		if _, ok := names[lower]; !ok {
			names[lower] = name
		}
		env[lower] = value
	}

	for _, name := range defaults.Inherit {
		if value, ok := parentEnv[strings.ToLower(name)]; ok {
			set(name, value)
		}
	}
	for key, value := range defaults.Vars {
		set(key, expand(value, parentEnv))
	}

	for _, file := range files {
		path, optional := parseFile(file)
		if err := storage.ValidatePath(path); err != nil {
			return nil, fmt.Errorf("invalid EnvironmentFile: %w", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if optional && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read EnvironmentFile %s: %w", path, err)
		}
		for lineNo, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			key = strings.TrimSpace(key)
			if !ok || key == "" {
				return nil, fmt.Errorf("%s:%d: invalid environment line", path, lineNo+1)
			}
			set(key, strings.TrimSpace(value))
		}
	}

	for key, value := range unitEnv {
		set(key, value)
	}

	result := make([]string, 0, len(env))
	for lower, value := range env {
		result = append(result, names[lower]+"="+value)
	}
	sort.Strings(result)
	return result, nil
}

// ValidateFiles returns warnings for EnvironmentFile= entries: a path that is
// not a plain absolute file path, or a file that cannot be read. A missing
// optional file is not a warning.
func ValidateFiles(files []string) []string {
	var warnings []string
	for _, file := range files {
		path, optional := parseFile(file)
		if err := storage.ValidatePath(path); err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if err := storage.InputReadable(path); err != nil {
			if optional && errors.Is(err, os.ErrNotExist) {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("environment file %s: %v", path, err))
		}
	}
	return warnings
}

// parseFile splits an EnvironmentFile= entry into its path and optional flag.
func parseFile(spec string) (path string, optional bool) {
	if strings.HasPrefix(spec, "-") {
		return strings.TrimPrefix(spec, "-"), true
	}
	return spec, false
}

// parent returns the parent process environment keyed case-insensitively.
func parent() map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok {
			env[strings.ToLower(key)] = value
		}
	}
	return env
}

// expand substitutes %NAME% placeholders from lookup (case-insensitive).
// "%%" yields a literal "%", an undefined name yields an empty string, and a
// "%" without a closing "%" is left as-is with a warning.
func expand(value string, lookup map[string]string) string {
	var b strings.Builder
	for i := 0; i < len(value); {
		if value[i] != '%' {
			b.WriteByte(value[i])
			i++
			continue
		}
		if i+1 < len(value) && value[i+1] == '%' {
			b.WriteByte('%')
			i += 2
			continue
		}
		end := strings.IndexByte(value[i+1:], '%')
		if end < 0 {
			slog.Warn("env: malformed environment placeholder", "value", value)
			b.WriteString(value[i:])
			break
		}
		name := value[i+1 : i+1+end]
		b.WriteString(lookup[strings.ToLower(name)])
		i += end + 2
	}
	return b.String()
}
