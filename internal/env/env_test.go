package env

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestExpand(t *testing.T) {
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
		if got := expand(input, lookup); got != want {
			t.Errorf("expand(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBuild(t *testing.T) {
	t.Setenv("MY_PARENT", "parent-value")
	unitEnv := map[string]string{"UNIT_VAR": "unit-value", "OVERRIDE": "from-unit"}
	defaults := Defaults{
		Inherit: []string{"MY_PARENT", "NOT_SET_VAR"},
		Vars: map[string]string{
			"DERIVED":  "pre-%MY_PARENT%-post",
			"OVERRIDE": "from-config",
		},
	}

	env := buildMap(t, unitEnv, nil, defaults)

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

func TestBuildCaseInsensitive(t *testing.T) {
	unitEnv := map[string]string{"OVERRIDE": "from-unit"}
	defaults := Defaults{Vars: map[string]string{"Override": "from-config"}}

	lines, err := Build(unitEnv, nil, defaults)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
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

func TestBuildFile(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "app.env")
	if err := os.WriteFile(envFile, []byte("FROM_FILE=filevalue\n# comment\n"), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	unitEnv := map[string]string{"FROM_UNIT": "unitvalue"}
	files := []string{envFile, "-" + filepath.Join(dir, "missing.env")}

	env := buildMap(t, unitEnv, files, Defaults{})

	if env["FROM_FILE"] != "filevalue" {
		t.Errorf("FROM_FILE = %q", env["FROM_FILE"])
	}
	if env["FROM_UNIT"] != "unitvalue" {
		t.Errorf("FROM_UNIT = %q", env["FROM_UNIT"])
	}
}

func TestRequiredFileMissing(t *testing.T) {
	files := []string{filepath.Join(t.TempDir(), "missing.env")}
	if _, err := Build(nil, files, Defaults{}); err == nil {
		t.Fatal("Build() error = nil")
	}
}

func TestFileInvalidPathRejected(t *testing.T) {
	separator := string(filepath.Separator)
	dotdot := t.TempDir() + separator + ".." + separator + "x.env"
	for _, file := range []string{"relative.env", "-relative.env", dotdot, "-" + dotdot} {
		if _, err := Build(nil, []string{file}, Defaults{}); err == nil {
			t.Errorf("Build() error = nil for EnvironmentFile %q, want error", file)
		}
	}
}

func buildMap(t *testing.T, unitEnv map[string]string, files []string, defaults Defaults) map[string]string {
	t.Helper()
	lines, err := Build(unitEnv, files, defaults)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	env := make(map[string]string)
	for _, line := range lines {
		key, value, _ := strings.Cut(line, "=")
		env[key] = value
	}
	return env
}
