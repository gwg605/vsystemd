package unit

import (
	"strings"
	"testing"
)

func TestParseID(t *testing.T) {
	tests := []struct {
		in   string
		want ID
	}{
		{"app.service", ID{Name: "app", Type: TypeService}},
		{"my-app_2.timer", ID{Name: "my-app_2", Type: TypeTimer}},
		{"App.SERVICE", ID{Name: "App", Type: TypeService}},
	}
	for _, tt := range tests {
		got, err := ParseID(tt.in)
		if err != nil {
			t.Errorf("ParseID(%q) error = %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseID(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseIDInvalid(t *testing.T) {
	tests := []string{
		"",
		".",
		"..",
		"../app.service",
		`sub\app.service`,
		"app",
		"app.txt",
		"my.app.service",
		"app .service",
		"app.service ",
		"app@.service",
		strings.Repeat("a", MaxNameLength+1) + ".service",
	}
	for _, in := range tests {
		if _, err := ParseID(in); err == nil {
			t.Errorf("ParseID(%q) error = nil", in)
		}
	}
}

func TestIDValidate(t *testing.T) {
	if err := (ID{}).Validate(); err == nil {
		t.Error("zero ID is valid")
	}
	if err := (ID{Name: "app", Type: TypeTimer}).Validate(); err != nil {
		t.Errorf("Validate() error = %v", err)
	}
	if got := (ID{Name: "app", Type: TypeService}).String(); got != "app.service" {
		t.Errorf("String() = %q", got)
	}
}
