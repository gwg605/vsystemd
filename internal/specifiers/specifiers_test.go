package specifiers

import "testing"

func testValues() Values {
	return Values{
		LogsDir:    `C:\data\logs`,
		StateDir:   `C:\data\state`,
		CacheDir:   `C:\data\cache`,
		ConfigDir:  `C:\data`,
		RuntimeDir: `C:\data\run`,
		Home:       `C:\Users\svc`,
		UnitName:   "app.service",
		UnitPrefix: "app",
	}
}

func TestExpand(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`%L`, `C:\data\logs`},
		{`%S`, `C:\data\state`},
		{`%C`, `C:\data\cache`},
		{`%E`, `C:\data`},
		{`%t`, `C:\data\run`},
		{`%h`, `C:\Users\svc`},
		{`%n`, "app.service"},
		{`%N`, "app"},
		{`%%`, `%`},
		{`append:%L\app\out.log`, `append:C:\data\logs\app\out.log`},
		{`no specifiers`, `no specifiers`},
	}
	for _, tt := range tests {
		got, err := Expand(tt.in, testValues())
		if err != nil {
			t.Errorf("Expand(%q) error = %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Expand(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExpandErrors(t *testing.T) {
	for _, in := range []string{`%X`, `%`, `C:\logs\%x\out`} {
		if _, err := Expand(in, testValues()); err == nil {
			t.Errorf("Expand(%q) error = nil", in)
		}
	}
}
