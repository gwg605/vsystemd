package unit

import "testing"

func TestResolveOutputs(t *testing.T) {
	stdout, stderr, err := ResolveOutputs(`file:C:\logs\out.log`, `append:C:\logs\out.log`)
	if err != nil {
		t.Fatalf("ResolveOutputs() error = %v", err)
	}
	if stdout.Kind != OutputFile || stderr.Kind != OutputAppend {
		t.Errorf("kinds = %v/%v", stdout.Kind, stderr.Kind)
	}
	if stdout.Path != `C:\logs\out.log` || stderr.Path != `C:\logs\out.log` {
		t.Errorf("paths = %q/%q", stdout.Path, stderr.Path)
	}

	stdout, stderr, err = ResolveOutputs("", "null")
	if err != nil {
		t.Fatalf("ResolveOutputs() error = %v", err)
	}
	if stdout.Kind != OutputDefault || stderr.Kind != OutputNull {
		t.Errorf("kinds = %v/%v", stdout.Kind, stderr.Kind)
	}
}

func TestResolveOutputsErrors(t *testing.T) {
	for _, value := range []string{"journal", "file:"} {
		if _, _, err := ResolveOutputs(value, ""); err == nil {
			t.Errorf("ResolveOutputs(%q) error = nil, want error", value)
		}
	}
}

func TestStatusTarget(t *testing.T) {
	file := OutputTarget{Kind: OutputFile, Path: `C:\a.log`}
	null := OutputTarget{Kind: OutputNull}

	if got := StatusTarget(file, null); got.Kind != OutputFile {
		t.Errorf("StatusTarget(file, null) = %v", got)
	}
	if got := StatusTarget(null, file); got.Kind != OutputFile {
		t.Errorf("StatusTarget(null, file) = %v", got)
	}
}
