package runner

import "strings"

func expandCommands(commands []command, lookup map[string]string) []command {
	for i := range commands {
		commands[i] = expandCommand(commands[i], lookup)
	}
	return commands
}

func expandCommand(cmd command, lookup map[string]string) command {
	cmd.path = expandEnvRefs(cmd.path, lookup)
	for i := range cmd.args {
		cmd.args[i] = expandEnvRefs(cmd.args[i], lookup)
	}
	return cmd
}

// expandEnvRefs substitutes ${NAME} references with values from lookup. An
// undefined name becomes an empty string; "$NAME" without braces is literal.
func expandEnvRefs(value string, lookup map[string]string) string {
	if !strings.Contains(value, "${") {
		return value
	}
	var b strings.Builder
	for i := 0; i < len(value); {
		if value[i] == '$' && i+1 < len(value) && value[i+1] == '{' {
			end := strings.IndexByte(value[i+2:], '}')
			if end >= 0 {
				name := value[i+2 : i+2+end]
				b.WriteString(lookup[strings.ToLower(name)])
				i += end + 3
				continue
			}
		}
		b.WriteByte(value[i])
		i++
	}
	return b.String()
}

func envListLookup(env []string) map[string]string {
	lookup := make(map[string]string, len(env))
	for _, entry := range env {
		if name, value, ok := strings.Cut(entry, "="); ok {
			lookup[strings.ToLower(name)] = value
		}
	}
	return lookup
}

// setEnv replaces or appends name in env, matching case-insensitively.
func setEnv(env []string, name, value string) []string {
	prefix := strings.ToLower(name) + "="
	for i, entry := range env {
		if strings.HasPrefix(strings.ToLower(entry), prefix) {
			env[i] = name + "=" + value
			return env
		}
	}
	return append(env, name+"="+value)
}
