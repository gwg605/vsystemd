package logwriter

import (
	"path/filepath"
	"testing"

	"github.com/gwg605/vsystemd/internal/unit"
)

func TestValidateUnitPath(t *testing.T) {
	valid := []string{
		`C:\ProgramData\vsystemd\logs\app\out.log`,
		`C:\a\b_2\out.log`,
		`C:\out.log`,
		`\\server\share\out.log`,
	}
	for _, path := range valid {
		if err := ValidateUnitPath(path); err != nil {
			t.Errorf("ValidateUnitPath(%q) error = %v", path, err)
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
		if err := ValidateUnitPath(path); err == nil {
			t.Errorf("ValidateUnitPath(%q) error = nil", path)
		}
	}
}

func TestResolveServiceOutputsTargetMustBeFile(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "missing", "out.log")
	if _, _, err := ResolveServiceOutputs(&unit.ServiceUnit{StandardOutput: "file:" + missing}); err == nil {
		t.Errorf("ResolveServiceOutputs(%q) error = nil, want error", missing)
	}

	if _, _, err := ResolveServiceOutputs(&unit.ServiceUnit{StandardOutput: "file:" + dir}); err == nil {
		t.Errorf("ResolveServiceOutputs(%q) error = nil, want error", dir)
	}

	file := filepath.Join(dir, "out.log")
	if _, _, err := ResolveServiceOutputs(&unit.ServiceUnit{StandardOutput: "file:" + file}); err != nil {
		t.Errorf("ResolveServiceOutputs(%q) error = %v", file, err)
	}
}
