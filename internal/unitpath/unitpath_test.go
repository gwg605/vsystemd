package unitpath

import (
	"testing"

	"github.com/gwg605/vsystemd/internal/specifiers"
	"github.com/gwg605/vsystemd/internal/unit"
)

func TestResolveService(t *testing.T) {
	service := &unit.ServiceUnit{
		WorkingDirectory: `%L\app`,
		StandardOutput:   `append:%L\app\out.log`,
		StandardError:    "null",
		Environment:      map[string]string{"LOG_DIR": `%L`},
	}
	values := specifiers.Values{LogsDir: `C:\data\logs`, UnitName: "app.service", UnitPrefix: "app"}

	got, err := ResolveService(service, values)
	if err != nil {
		t.Fatalf("ResolveService() error = %v", err)
	}
	if got.WorkingDirectory != `C:\data\logs\app` {
		t.Errorf("WorkingDirectory = %q", got.WorkingDirectory)
	}
	if got.StandardOutput != `append:C:\data\logs\app\out.log` {
		t.Errorf("StandardOutput = %q", got.StandardOutput)
	}
	if got.StandardError != "null" {
		t.Errorf("StandardError = %q", got.StandardError)
	}
	if got.Environment["LOG_DIR"] != `C:\data\logs` {
		t.Errorf("Environment = %v", got.Environment)
	}
	if service.StandardOutput != `append:%L\app\out.log` {
		t.Errorf("input was modified: %q", service.StandardOutput)
	}
}

func TestResolveServiceError(t *testing.T) {
	service := &unit.ServiceUnit{StandardOutput: "file:%X"}
	if _, err := ResolveService(service, specifiers.Values{}); err == nil {
		t.Fatal("ResolveService() error = nil")
	}
}
