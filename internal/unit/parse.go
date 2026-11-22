package unit

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	systemdtime "gitlab.com/allddd/go-systemd-time"
)

func Parse(id ID, data []byte) (*UnitFile, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}

	file := &UnitFile{Name: id}
	if id.Type == TypeService {
		file.Service = &ServiceUnit{
			Restart:              RestartNo,
			Environment:          make(map[string]string),
			LogsDirectoryMode:    defaultDirectoryMode,
			RuntimeDirectoryMode: defaultDirectoryMode,
		}
	} else {
		file.Timer = &TimerUnit{}
	}

	var (
		section     string
		seenService bool
		seenTimer   bool
	)

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}

		if line[0] == '[' {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("%s:%d: malformed section header", id, lineNo)
			}
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			switch section {
			case "service":
				if id.Type != TypeService {
					return nil, fmt.Errorf("%s:%d: [Service] is not allowed in a timer unit", id, lineNo)
				}
				seenService = true
			case "timer":
				if id.Type != TypeTimer {
					return nil, fmt.Errorf("%s:%d: [Timer] is not allowed in a service unit", id, lineNo)
				}
				seenTimer = true
			}
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected key=value", id, lineNo)
		}
		if err := file.apply(section, strings.TrimSpace(key), strings.TrimSpace(value)); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", id, lineNo, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if id.Type == TypeService && !seenService {
		return nil, fmt.Errorf("%s: missing [Service] section", id)
	}
	if id.Type == TypeTimer && !seenTimer {
		return nil, fmt.Errorf("%s: missing [Timer] section", id)
	}
	return file, nil
}

func (u *UnitFile) apply(section, key, value string) error {
	switch section {
	case "unit":
		u.applyUnit(key, value)
	case "service":
		return u.Service.apply(key, value)
	case "timer":
		return u.applyTimer(key, value)
	}
	return nil
}

func (u *UnitFile) applyUnit(key, value string) {
	switch key {
	case "Description":
		u.Description = value
	case "After":
		u.After = append(u.After, strings.Fields(value)...)
	case "Requires":
		u.Requires = append(u.Requires, strings.Fields(value)...)
	case "Wants":
		u.Wants = append(u.Wants, strings.Fields(value)...)
		u.warn("Wants= is ignored")
	case "Before", "BindsTo", "PartOf", "Conflicts":
		u.warn(fmt.Sprintf("%s= is not supported", key))
	}
}

func (u *UnitFile) warn(message string) {
	for _, existing := range u.Warnings {
		if existing == message {
			return
		}
	}
	u.Warnings = append(u.Warnings, message)
}

func (s *ServiceUnit) apply(key, value string) error {
	switch key {
	case "ExecStart":
		s.ExecStart = value
	case "ExecStartPre":
		s.ExecStartPre = append(s.ExecStartPre, value)
	case "ExecStartPost":
		s.ExecStartPost = append(s.ExecStartPost, value)
	case "ExecStop":
		s.ExecStop = value
	case "Restart":
		switch RestartPolicy(value) {
		case RestartNo, RestartAlways, RestartOnFailure:
			s.Restart = RestartPolicy(value)
		case "":
			s.Restart = RestartNo
		default:
			return fmt.Errorf("invalid Restart value %q", value)
		}
	case "RestartSec":
		d, err := parseTimespan(value)
		if err != nil {
			return fmt.Errorf("invalid RestartSec: %w", err)
		}
		s.RestartSec = d
	case "TimeoutStopSec":
		d, err := parseTimespan(value)
		if err != nil {
			return fmt.Errorf("invalid TimeoutStopSec: %w", err)
		}
		s.TimeoutStopSec = d
	case "WorkingDirectory":
		s.WorkingDirectory = value
	case "Environment":
		env, err := parseEnvironment(value)
		if err != nil {
			return err
		}
		for name, val := range env {
			s.Environment[name] = val
		}
	case "EnvironmentFile":
		s.EnvironmentFiles = append(s.EnvironmentFiles, strings.Fields(value)...)
	case "User":
		s.User = value
	case "Group":
		s.Group = value
	case "StandardOutput":
		s.StandardOutput = value
	case "StandardError":
		s.StandardError = value
	case "LogsDirectory":
		s.LogsDirectory = append(s.LogsDirectory, strings.Fields(value)...)
	case "LogsDirectoryMode":
		mode, err := parseMode(value)
		if err != nil {
			return fmt.Errorf("invalid LogsDirectoryMode: %w", err)
		}
		s.LogsDirectoryMode = mode
	case "RuntimeDirectory":
		s.RuntimeDirectory = append(s.RuntimeDirectory, strings.Fields(value)...)
	case "RuntimeDirectoryMode":
		mode, err := parseMode(value)
		if err != nil {
			return fmt.Errorf("invalid RuntimeDirectoryMode: %w", err)
		}
		s.RuntimeDirectoryMode = mode
	case "StartLimitBurst":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("invalid StartLimitBurst value %q", value)
		}
		s.StartLimitBurst = n
	case "StartLimitIntervalSec":
		d, err := parseTimespan(value)
		if err != nil {
			return fmt.Errorf("invalid StartLimitIntervalSec: %w", err)
		}
		s.StartLimitIntervalSec = d
	}
	return nil
}

func (u *UnitFile) applyTimer(key, value string) error {
	t := u.Timer
	switch key {
	case "OnCalendar":
		t.OnCalendar = append(t.OnCalendar, value)
	case "OnBootSec":
		d, err := parseTimespan(value)
		if err != nil {
			return fmt.Errorf("invalid OnBootSec: %w", err)
		}
		t.OnBootSec = d
		u.warn("OnBootSec= is not supported")
	case "OnStartupSec":
		d, err := parseTimespan(value)
		if err != nil {
			return fmt.Errorf("invalid OnStartupSec: %w", err)
		}
		t.OnStartupSec = d
		u.warn("OnStartupSec= is not supported")
	case "Unit":
		t.Unit = value
	default:
		u.warn(fmt.Sprintf("unsupported timer directive %s", key))
	}
	return nil
}

func parseMode(value string) (os.FileMode, error) {
	n, err := strconv.ParseUint(value, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid octal mode %q", value)
	}
	if n > 0o7777 {
		return 0, fmt.Errorf("mode %q out of range", value)
	}
	return os.FileMode(n & 0o777), nil
}

func parseTimespan(value string) (time.Duration, error) {
	d, err := systemdtime.ParseTimespan(value)
	if err != nil {
		return 0, fmt.Errorf("invalid time span %q: %w", value, err)
	}
	return d, nil
}

func parseEnvironment(value string) (map[string]string, error) {
	env := make(map[string]string)
	for _, assignment := range strings.Fields(value) {
		key, val, ok := strings.Cut(assignment, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid environment assignment %q", assignment)
		}
		env[key] = val
	}
	return env, nil
}
