package unitpath

import (
	"fmt"
	"os"

	"github.com/gwg605/vsystemd/internal/specifiers"
	"github.com/gwg605/vsystemd/internal/unit"
)

// Roots are the base directories referenced by path specifiers.
type Roots struct {
	LogsDir    string
	StateDir   string
	CacheDir   string
	ConfigDir  string
	RuntimeDir string
}

// Service holds the resolved path-like settings of a service unit.
type Service struct {
	WorkingDirectory string
	StandardOutput   string
	StandardError    string
	Environment      map[string]string
}

// NewValues builds the specifier values for a unit.
func NewValues(roots Roots, id unit.ID) specifiers.Values {
	home, _ := os.UserHomeDir()
	return specifiers.Values{
		LogsDir:    roots.LogsDir,
		StateDir:   roots.StateDir,
		CacheDir:   roots.CacheDir,
		ConfigDir:  roots.ConfigDir,
		RuntimeDir: roots.RuntimeDir,
		Home:       home,
		UnitName:   id.String(),
		UnitPrefix: id.Name,
	}
}

// ResolveService expands systemd-style specifiers in the path-like settings and
// in the Environment= values of a service unit. EnvironmentFile= contents are
// not expanded. It does not modify the input unit.
func ResolveService(service *unit.ServiceUnit, values specifiers.Values) (Service, error) {
	result := Service{Environment: make(map[string]string, len(service.Environment))}
	var err error
	if result.WorkingDirectory, err = specifiers.Expand(service.WorkingDirectory, values); err != nil {
		return Service{}, fmt.Errorf("WorkingDirectory: %w", err)
	}
	if result.StandardOutput, err = specifiers.Expand(service.StandardOutput, values); err != nil {
		return Service{}, fmt.Errorf("StandardOutput: %w", err)
	}
	if result.StandardError, err = specifiers.Expand(service.StandardError, values); err != nil {
		return Service{}, fmt.Errorf("StandardError: %w", err)
	}
	for key, value := range service.Environment {
		expanded, err := specifiers.Expand(value, values)
		if err != nil {
			return Service{}, fmt.Errorf("Environment %s: %w", key, err)
		}
		result.Environment[key] = expanded
	}
	return result, nil
}
