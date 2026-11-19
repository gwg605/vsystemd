package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePath(t *testing.T) {
	valid := []string{
		`C:\ProgramData\vsystemd\logs\app\out.log`,
		`C:\a\b_2\out.log`,
		`C:\out.log`,
		`\\server\share\out.log`,
	}
	for _, path := range valid {
		if err := ValidatePath(path); err != nil {
			t.Errorf("ValidatePath(%q) error = %v", path, err)
		}
	}

	invalid := []string{
		`logs\out.log`,
		`C:\..\out.log`,
		`C:\a\.. \out.log`,
		`C:\a\...\out.log`,
		`C:\a\foo. \out.log`,
		`C:\a\foo.\out.log`,
		`C:\a\out.txt:stream`,
		`\\.\pipe\x`,
		`\\?\C:\a\out.log`,
		`C:\NUL\out.log`,
		`C:\a\aux.log`,
		`C:\a\COM1.log`,
	}
	for _, path := range invalid {
		if err := ValidatePath(path); err == nil {
			t.Errorf("ValidatePath(%q) error = nil", path)
		}
	}
}

func TestOutputWritable(t *testing.T) {
	dir := t.TempDir()
	if err := OutputWritable(filepath.Join(dir, "out.log")); err != nil {
		t.Errorf("OutputWritable() error = %v", err)
	}
	if err := OutputWritable(filepath.Join(dir, "missing", "out.log")); err == nil {
		t.Error("OutputWritable() error = nil for a missing directory")
	}
}

func TestInputReadable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "app.env")
	if err := os.WriteFile(file, []byte("A=B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InputReadable(file); err != nil {
		t.Errorf("InputReadable() error = %v", err)
	}
	if err := InputReadable(filepath.Join(t.TempDir(), "missing.env")); err == nil {
		t.Error("InputReadable() error = nil for a missing file")
	}
}
