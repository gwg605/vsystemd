package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/unit"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
)

func TestProcessHelper(t *testing.T) {
	args, ok := helperArgs(os.Args)
	if !ok {
		return
	}

	switch args[0] {
	case "record":
		code := 0
		if len(args) > 3 {
			code, _ = strconv.Atoi(args[3])
		}
		file, err := os.OpenFile(args[1], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			os.Exit(2)
		}
		fmt.Fprintln(file, args[2])
		file.Close()
		os.Exit(code)
	case "recordenv":
		if err := os.WriteFile(args[1], []byte(os.Getenv(args[2])), 0o644); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	case "recordcwd":
		wd, err := os.Getwd()
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(args[1], []byte(wd), 0o644); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	case "output":
		fmt.Fprint(os.Stdout, args[1])
		fmt.Fprint(os.Stderr, args[2])
		os.Exit(0)
	case "failonce":
		if _, err := os.Stat(args[1]); err == nil {
			os.Exit(0)
		}
		if err := os.WriteFile(args[1], []byte("failed"), 0o644); err != nil {
			os.Exit(2)
		}
		os.Exit(1)
	case "sleep":
		duration, err := time.ParseDuration(args[1])
		if err != nil {
			os.Exit(2)
		}
		time.Sleep(duration)
		os.Exit(0)
	}
	os.Exit(2)
}

func helperArgs(argv []string) ([]string, bool) {
	index := 0
	for index < len(argv) && argv[index] != "--" {
		index++
	}
	if index+1 >= len(argv) {
		return nil, false
	}
	return argv[index+1:], true
}

func helperCommand(args ...string) string {
	parts := []string{quoteArg(os.Args[0]), "-test.run=TestProcessHelper", "--"}
	for _, arg := range args {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteArg(arg string) string {
	if arg == "" || strings.ContainsAny(arg, " \t") {
		return `"` + arg + `"`
	}
	return arg
}

func TestWriteStopConfirmation(t *testing.T) {
	var buffer strings.Builder
	if err := writeStopConfirmation(&buffer); err != nil {
		t.Fatalf("writeStopConfirmation() error = %v", err)
	}
	if buffer.String() != "Y\r\n" {
		t.Errorf("stdin = %q", buffer.String())
	}
}

func TestParseCommand(t *testing.T) {
	cmd, err := parseCommand(`-"C:\Program Files\app.exe" --flag "hello world"`)
	if err != nil {
		t.Fatalf("parseCommand() error = %v", err)
	}
	if !cmd.ignoreFailure {
		t.Error("ignoreFailure = false")
	}
	if cmd.path != `C:\Program Files\app.exe` {
		t.Errorf("path = %q", cmd.path)
	}
	if !reflect.DeepEqual(cmd.args, []string{"--flag", "hello world"}) {
		t.Errorf("args = %v", cmd.args)
	}
}

func TestExpandEnv(t *testing.T) {
	lookup := map[string]string{"path": `C:\bin`, "name": "app"}
	tests := map[string]string{
		"%PATH%":     `C:\bin`,
		"x-%NAME%-y": "x-app-y",
		"%MISSING%":  "",
		"100%%":      "100%",
		"%%PATH%%":   "%PATH%",
		"%PATH":      "%PATH",
		"plain":      "plain",
	}
	for input, want := range tests {
		if got := expandEnv(input, lookup); got != want {
			t.Errorf("expandEnv(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBuildEnvironment(t *testing.T) {
	t.Setenv("MY_PARENT", "parent-value")
	service := &unit.ServiceUnit{
		Environment: map[string]string{"UNIT_VAR": "unit-value", "OVERRIDE": "from-unit"},
	}
	defaults := environmentDefaults{
		Inherit: []string{"MY_PARENT", "NOT_SET_VAR"},
		Vars: map[string]string{
			"DERIVED":  "pre-%MY_PARENT%-post",
			"OVERRIDE": "from-config",
		},
	}

	env := environmentMap(t, service, defaults)

	if env["MY_PARENT"] != "parent-value" {
		t.Errorf("MY_PARENT = %q", env["MY_PARENT"])
	}
	if _, ok := env["NOT_SET_VAR"]; ok {
		t.Errorf("NOT_SET_VAR should not be set")
	}
	if env["DERIVED"] != "pre-parent-value-post" {
		t.Errorf("DERIVED = %q", env["DERIVED"])
	}
	if env["OVERRIDE"] != "from-unit" {
		t.Errorf("OVERRIDE = %q, want unit value to win", env["OVERRIDE"])
	}
	if env["UNIT_VAR"] != "unit-value" {
		t.Errorf("UNIT_VAR = %q", env["UNIT_VAR"])
	}
}

func TestBuildEnvironmentCaseInsensitive(t *testing.T) {
	service := &unit.ServiceUnit{
		Environment: map[string]string{"OVERRIDE": "from-unit"},
	}
	defaults := environmentDefaults{
		Vars: map[string]string{"Override": "from-config"},
	}

	lines, err := buildEnvironment(storagefake.New(), service, defaults)
	if err != nil {
		t.Fatalf("buildEnvironment() error = %v", err)
	}
	env := make(map[string]string)
	for _, line := range lines {
		key, value, _ := strings.Cut(line, "=")
		env[key] = value
	}

	if env["Override"] != "from-unit" {
		t.Errorf("Override = %q, want the unit value and the first-seen name", env["Override"])
	}
	if _, ok := env["OVERRIDE"]; ok {
		t.Error("duplicate OVERRIDE entry present")
	}
	if !sort.StringsAreSorted(lines) {
		t.Errorf("environment is not sorted: %v", lines)
	}
}

func TestBuildEnvironmentFile(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "app.env")
	if err := os.WriteFile(envFile, []byte("FROM_FILE=filevalue\n# comment\n"), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	service := &unit.ServiceUnit{
		EnvironmentFiles: []string{envFile, "-" + filepath.Join(dir, "missing.env")},
		Environment:      map[string]string{"FROM_UNIT": "unitvalue"},
	}

	env := environmentMap(t, service, environmentDefaults{})

	if env["FROM_FILE"] != "filevalue" {
		t.Errorf("FROM_FILE = %q", env["FROM_FILE"])
	}
	if env["FROM_UNIT"] != "unitvalue" {
		t.Errorf("FROM_UNIT = %q", env["FROM_UNIT"])
	}
}

func TestRequiredEnvironmentFileMissing(t *testing.T) {
	service := &unit.ServiceUnit{
		EnvironmentFiles: []string{filepath.Join(t.TempDir(), "missing.env")},
	}
	if _, err := buildEnvironment(storagefake.New(), service, environmentDefaults{}); err == nil {
		t.Fatal("buildEnvironment() error = nil")
	}
}

func TestEnvironmentFileInvalidPathRejected(t *testing.T) {
	separator := string(filepath.Separator)
	dotdot := t.TempDir() + separator + ".." + separator + "x.env"
	for _, file := range []string{"relative.env", "-relative.env", dotdot, "-" + dotdot} {
		service := &unit.ServiceUnit{EnvironmentFiles: []string{file}}
		if _, err := buildEnvironment(storagefake.New(), service, environmentDefaults{}); err == nil {
			t.Errorf("buildEnvironment() error = nil for EnvironmentFile %q, want error", file)
		}
	}
}

func environmentMap(t *testing.T, service *unit.ServiceUnit, defaults environmentDefaults) map[string]string {
	t.Helper()
	lines, err := buildEnvironment(storagefake.New(), service, defaults)
	if err != nil {
		t.Fatalf("buildEnvironment() error = %v", err)
	}
	env := make(map[string]string)
	for _, line := range lines {
		key, value, _ := strings.Cut(line, "=")
		env[key] = value
	}
	return env
}
