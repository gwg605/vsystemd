package runner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/unit"
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

func newProcessManager(service *unit.ServiceUnit) *ProcessManager {
	return NewProcessManager(service, EnvironmentDefaults{})
}

func TestInterruptNotRunning(t *testing.T) {
	manager := newProcessManager(&unit.ServiceUnit{ExecStart: helperCommand("sleep", "100ms")})
	if err := manager.Interrupt(); err == nil {
		t.Error("Interrupt() error = nil for a non-running process")
	}
}

func TestStartSequence(t *testing.T) {
	dir := t.TempDir()
	orderFile := filepath.Join(dir, "order.txt")

	manager := newProcessManager(&unit.ServiceUnit{
		ExecStartPre:  []string{helperCommand("record", orderFile, "pre")},
		ExecStart:     helperCommand("sleep", "100ms"),
		ExecStartPost: []string{helperCommand("record", orderFile, "post")},
	})

	if err := manager.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if manager.PID() == 0 {
		t.Error("PID() = 0 after start")
	}
	code, err := manager.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if code != 0 {
		t.Errorf("Wait() = %d", code)
	}

	data, err := os.ReadFile(orderFile)
	if err != nil {
		t.Fatalf("read order file: %v", err)
	}
	if string(data) != "pre\npost\n" {
		t.Errorf("order = %q", data)
	}
}

func TestIgnoredExecFailure(t *testing.T) {
	dir := t.TempDir()
	orderFile := filepath.Join(dir, "order.txt")

	manager := newProcessManager(&unit.ServiceUnit{
		ExecStartPre: []string{"-" + helperCommand("record", orderFile, "pre", "1")},
		ExecStart:    helperCommand("record", orderFile, "start"),
	})

	if err := manager.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	code, err := manager.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if code != 0 {
		t.Errorf("Wait() = %d", code)
	}

	data, err := os.ReadFile(orderFile)
	if err != nil {
		t.Fatalf("read order file: %v", err)
	}
	if string(data) != "pre\nstart\n" {
		t.Errorf("order = %q", data)
	}
}

func TestExecStartPreFailure(t *testing.T) {
	dir := t.TempDir()
	orderFile := filepath.Join(dir, "order.txt")

	manager := newProcessManager(&unit.ServiceUnit{
		ExecStartPre: []string{helperCommand("record", orderFile, "pre", "1")},
		ExecStart:    helperCommand("record", orderFile, "start"),
	})

	if err := manager.Start(); err == nil {
		t.Fatal("Start() error = nil")
	}

	data, err := os.ReadFile(orderFile)
	if err != nil {
		t.Fatalf("read order file: %v", err)
	}
	if string(data) != "pre\n" {
		t.Errorf("order = %q", data)
	}
}

func TestEnvironment(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "app.env")
	if err := os.WriteFile(envFile, []byte("FROM_FILE=filevalue\n# comment\n"), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	fileValue := filepath.Join(dir, "file.txt")
	unitValue := filepath.Join(dir, "unit.txt")

	manager := newProcessManager(&unit.ServiceUnit{
		EnvironmentFiles: []string{envFile, "-" + filepath.Join(dir, "missing.env")},
		Environment:      map[string]string{"FROM_UNIT": "unitvalue"},
		ExecStartPre: []string{
			helperCommand("recordenv", fileValue, "FROM_FILE"),
			helperCommand("recordenv", unitValue, "FROM_UNIT"),
		},
		ExecStart: helperCommand("sleep", "10ms"),
	})
	if err := manager.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := manager.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	for name, want := range map[string]string{fileValue: "filevalue", unitValue: "unitvalue"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(data) != want {
			t.Errorf("%s = %q, want %q", name, data, want)
		}
	}
}

func TestRequiredEnvironmentFileMissing(t *testing.T) {
	manager := newProcessManager(&unit.ServiceUnit{
		EnvironmentFiles: []string{filepath.Join(t.TempDir(), "missing.env")},
		ExecStart:        "cmd /c exit 0",
	})
	if err := manager.Start(); err == nil {
		t.Fatal("Start() error = nil")
	}
}

func TestEnvironmentFileInvalidPathRejected(t *testing.T) {
	dotdot := t.TempDir() + string(filepath.Separator) + ".." + string(filepath.Separator) + "x.env"
	for _, file := range []string{"relative.env", "-relative.env", dotdot, "-" + dotdot} {
		manager := newProcessManager(&unit.ServiceUnit{
			EnvironmentFiles: []string{file},
			ExecStart:        "x",
		})
		if err := manager.Start(); err == nil {
			t.Errorf("Start() error = nil for EnvironmentFile %q, want error", file)
		}
	}
}

func TestOptionalEnvironmentFileMissing(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran.txt")
	manager := newProcessManager(&unit.ServiceUnit{
		EnvironmentFiles: []string{"-" + filepath.Join(t.TempDir(), "missing.env")},
		ExecStart:        helperCommand("record", marker, "ran"),
	})
	if err := manager.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := manager.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if strings.TrimSpace(string(data)) != "ran" {
		t.Errorf("marker = %q, want \"ran\"", data)
	}
}

func TestWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "cwd.txt")

	manager := newProcessManager(&unit.ServiceUnit{
		WorkingDirectory: dir,
		ExecStart:        helperCommand("recordcwd", output),
	})
	if err := manager.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := manager.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read cwd: %v", err)
	}
	if string(data) != dir {
		t.Errorf("cwd = %q, want %q", data, dir)
	}
}

func TestMissingExecStart(t *testing.T) {
	manager := newProcessManager(&unit.ServiceUnit{})
	if err := manager.Start(); err == nil {
		t.Fatal("Start() error = nil")
	}
}

func TestWriteStopConfirmation(t *testing.T) {
	var buffer bytes.Buffer
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
	defaults := EnvironmentDefaults{
		Inherit: []string{"MY_PARENT", "NOT_SET_VAR"},
		Vars: map[string]string{
			"DERIVED":  "pre-%MY_PARENT%-post",
			"OVERRIDE": "from-config",
		},
	}

	lines, err := buildEnvironment(service, defaults)
	if err != nil {
		t.Fatalf("buildEnvironment() error = %v", err)
	}
	env := make(map[string]string)
	for _, line := range lines {
		key, value, _ := strings.Cut(line, "=")
		env[key] = value
	}

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
