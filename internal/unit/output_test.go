package unit

import "testing"

func TestResolveOutputs(t *testing.T) {
	service := &ServiceUnit{
		StandardOutput: `file:C:\logs\out.log`,
		StandardError:  `append:C:\logs\out.log`,
	}
	stdout, stderr, err := service.ResolveOutputs()
	if err != nil {
		t.Fatalf("ResolveOutputs() error = %v", err)
	}
	if stdout.Kind != OutputFile || stderr.Kind != OutputAppend {
		t.Errorf("kinds = %v/%v", stdout.Kind, stderr.Kind)
	}
	if stdout.Path != `C:\logs\out.log` || stderr.Path != `C:\logs\out.log` {
		t.Errorf("paths = %q/%q", stdout.Path, stderr.Path)
	}

	service = &ServiceUnit{StandardOutput: "", StandardError: "null"}
	stdout, stderr, err = service.ResolveOutputs()
	if err != nil {
		t.Fatalf("ResolveOutputs() error = %v", err)
	}
	if stdout.Kind != OutputDefault || stderr.Kind != OutputNull {
		t.Errorf("kinds = %v/%v", stdout.Kind, stderr.Kind)
	}
}

func TestResolveOutputsErrors(t *testing.T) {
	for _, value := range []string{"journal", "file:"} {
		service := &ServiceUnit{StandardOutput: value}
		if _, _, err := service.ResolveOutputs(); err == nil {
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
