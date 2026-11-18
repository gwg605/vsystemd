package runner

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/unit"
)

// EnvironmentDefaults describes how the clean environment for service
// processes is built: Inherit lists variables taken from the runner's
// environment, Vars are extra variables whose values may reference the runner's
// environment via %NAME% placeholders.
type EnvironmentDefaults struct {
	Inherit []string
	Vars    map[string]string
}

func buildEnvironment(service *unit.ServiceUnit, defaults EnvironmentDefaults) ([]string, error) {
	parent := parentEnvironment()

	env := make(map[string]string)
	for _, name := range defaults.Inherit {
		if value, ok := parent[strings.ToLower(name)]; ok {
			env[name] = value
		}
	}
	for key, value := range defaults.Vars {
		env[key] = expandEnv(value, parent)
	}

	for _, file := range service.EnvironmentFiles {
		optional := strings.HasPrefix(file, "-")
		path := strings.TrimPrefix(file, "-")
		if err := logwriter.ValidateUnitPath(path); err != nil {
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
			env[key] = strings.TrimSpace(value)
		}
	}

	for key, value := range service.Environment {
		env[key] = value
	}

	result := make([]string, 0, len(env))
	for key, value := range env {
		result = append(result, key+"="+value)
	}
	return result, nil
}

// parentEnvironment returns the runner's environment keyed case-insensitively.
func parentEnvironment() map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok {
			env[strings.ToLower(key)] = value
		}
	}
	return env
}

// expandEnv substitutes %NAME% placeholders from lookup (case-insensitive).
// "%%" yields a literal "%", an undefined name yields an empty string, and a
// "%" without a closing "%" is left as-is with a warning.
func expandEnv(value string, lookup map[string]string) string {
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
			slog.Warn("runner: malformed environment placeholder", "value", value)
			b.WriteString(value[i:])
			break
		}
		name := value[i+1 : i+1+end]
		b.WriteString(lookup[strings.ToLower(name)])
		i += end + 2
	}
	return b.String()
}
