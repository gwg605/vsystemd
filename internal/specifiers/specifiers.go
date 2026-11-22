package specifiers

import (
	"fmt"
	"strings"
)

// Values holds the runtime and unit values referenced by specifiers.
type Values struct {
	LogsDir    string
	StateDir   string
	CacheDir   string
	ConfigDir  string
	RuntimeDir string
	Home       string
	UnitName   string
	UnitPrefix string
}

// Expand replaces systemd-style specifiers ("%L", "%n", ..., "%%") in value. An
// unknown specifier or a trailing "%" is an error, matching systemd where an
// unresolvable specifier makes the setting invalid.
func Expand(value string, values Values) (string, error) {
	if !strings.ContainsRune(value, '%') {
		return value, nil
	}

	var b strings.Builder
	for i := 0; i < len(value); {
		if value[i] != '%' {
			b.WriteByte(value[i])
			i++
			continue
		}
		if i+1 >= len(value) {
			return "", fmt.Errorf("trailing %% in %q", value)
		}
		replacement, ok := lookup(value[i+1], values)
		if !ok {
			return "", fmt.Errorf("unknown specifier %%%c in %q", value[i+1], value)
		}
		b.WriteString(replacement)
		i += 2
	}
	return b.String(), nil
}

func lookup(spec byte, values Values) (string, bool) {
	switch spec {
	case '%':
		return "%", true
	case 'L':
		return values.LogsDir, true
	case 'S':
		return values.StateDir, true
	case 'C':
		return values.CacheDir, true
	case 'E':
		return values.ConfigDir, true
	case 't':
		return values.RuntimeDir, true
	case 'h':
		return values.Home, true
	case 'n':
		return values.UnitName, true
	case 'N':
		return values.UnitPrefix, true
	default:
		return "", false
	}
}
