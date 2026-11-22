package runner

import (
	"path/filepath"
	"testing"

	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
)

func TestResolveLogsDirectories(t *testing.T) {
	store := storagefake.New()
	base := t.TempDir()

	got, err := resolveLogsDirectories(store, base, []string{"app", "access"})
	if err != nil {
		t.Fatalf("resolveLogsDirectories() error = %v", err)
	}
	want := []string{filepath.Join(base, "app"), filepath.Join(base, "access")}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dirs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveLogsDirectoriesInvalid(t *testing.T) {
	store := storagefake.New()
	base := t.TempDir()

	for _, name := range []string{"", ".", "..", `sub\dir`, "sub/dir", `C:\abs`} {
		if _, err := resolveLogsDirectories(store, base, []string{name}); err == nil {
			t.Errorf("resolveLogsDirectories(%q) error = nil", name)
		}
	}
}

func TestExpandEnvRefs(t *testing.T) {
	lookup := map[string]string{"logs_directory": `C:\data\logs`, "name": "app"}

	tests := []struct {
		in   string
		want string
	}{
		{`${LOGS_DIRECTORY}\out.log`, `C:\data\logs\out.log`},
		{`--dir=${LOGS_DIRECTORY}`, `--dir=C:\data\logs`},
		{`${MISSING}x`, "x"},
		{`$LOGS_DIRECTORY`, `$LOGS_DIRECTORY`},
		{`plain`, "plain"},
	}
	for _, tt := range tests {
		if got := expandEnvRefs(tt.in, lookup); got != tt.want {
			t.Errorf("expandEnvRefs(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSetEnv(t *testing.T) {
	env := []string{"Path=C:\\Windows", "LOGS_DIRECTORY=old"}
	env = setEnv(env, "LOGS_DIRECTORY", "new")
	if len(env) != 2 || env[1] != "LOGS_DIRECTORY=new" {
		t.Errorf("setEnv replace = %v", env)
	}

	env = setEnv([]string{"Path=C:\\Windows"}, "LOGS_DIRECTORY", "new")
	if len(env) != 2 || env[1] != "LOGS_DIRECTORY=new" {
		t.Errorf("setEnv append = %v", env)
	}
}
