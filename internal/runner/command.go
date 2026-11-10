package runner

import (
	"fmt"
	"strings"
)

type command struct {
	path          string
	args          []string
	ignoreFailure bool
}

func parseCommands(lines []string) ([]command, error) {
	commands := make([]command, 0, len(lines))
	for _, line := range lines {
		cmd, err := parseCommand(line)
		if err != nil {
			return nil, err
		}
		if cmd != nil {
			commands = append(commands, *cmd)
		}
	}
	return commands, nil
}

func parseCommand(line string) (*command, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil
	}

	ignoreFailure := false
	if strings.HasPrefix(line, "-") {
		ignoreFailure = true
		line = strings.TrimSpace(line[1:])
	}

	fields, err := splitCommandLine(line)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return &command{
		path:          fields[0],
		args:          fields[1:],
		ignoreFailure: ignoreFailure,
	}, nil
}

func splitCommandLine(line string) ([]string, error) {
	var (
		fields  []string
		current strings.Builder
		quote   rune
		inField bool
	)

	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			inField = true
		case r == '\'' || r == '"':
			quote = r
			inField = true
		case r == ' ' || r == '\t':
			if inField {
				fields = append(fields, current.String())
				current.Reset()
				inField = false
			}
		default:
			current.WriteRune(r)
			inField = true
		}
	}

	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in %q", line)
	}
	if inField {
		fields = append(fields, current.String())
	}
	return fields, nil
}
