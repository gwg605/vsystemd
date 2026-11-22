package unit

import (
	"reflect"
	"testing"
	"time"
)

func TestParseService(t *testing.T) {
	data := []byte(`# Example service
[Unit]
Description=Example service
After=network.target
Requires=db.service
Wants=redis.service

[Service]
ExecStartPre=/app/pre --check
ExecStartPre=-/app/pre2
ExecStart=/app/server --port 8080
ExecStartPost=/app/post
ExecStop=/app/stop
Restart=on-failure
RestartSec=5s
TimeoutStopSec=1min
WorkingDirectory=C:\app
Environment=FOO=bar BAZ=qux
Environment=EXTRA=value
EnvironmentFile=C:\app\.env
EnvironmentFile=-C:\app\.env.local
User=NT AUTHORITY\LocalService
StandardOutput=file:C:\logs\out.log
StandardError=file:C:\logs\err.log
LogsDirectory=app-logs app-data
LogsDirectoryMode=0750
StartLimitBurst=3
StartLimitIntervalSec=30s
`)

	file, err := Parse(ID{Name: "example", Type: TypeService}, data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if file.Name.String() != "example.service" {
		t.Errorf("Name = %q", file.Name)
	}
	if file.Description != "Example service" {
		t.Errorf("Description = %q", file.Description)
	}
	if !reflect.DeepEqual(file.After, []string{"network.target"}) {
		t.Errorf("After = %v", file.After)
	}
	if !reflect.DeepEqual(file.Requires, []string{"db.service"}) {
		t.Errorf("Requires = %v", file.Requires)
	}
	if !reflect.DeepEqual(file.Wants, []string{"redis.service"}) {
		t.Errorf("Wants = %v", file.Wants)
	}
	if !reflect.DeepEqual(file.Warnings, []string{"Wants= is ignored", "LogsDirectoryMode= is ignored"}) {
		t.Errorf("Warnings = %v", file.Warnings)
	}
	if file.Timer != nil {
		t.Error("Timer should be nil")
	}

	service := file.Service
	if service == nil {
		t.Fatal("Service is nil")
	}
	if service.ExecStart != "/app/server --port 8080" {
		t.Errorf("ExecStart = %q", service.ExecStart)
	}
	if !reflect.DeepEqual(service.ExecStartPre, []string{"/app/pre --check", "-/app/pre2"}) {
		t.Errorf("ExecStartPre = %v", service.ExecStartPre)
	}
	if !reflect.DeepEqual(service.ExecStartPost, []string{"/app/post"}) {
		t.Errorf("ExecStartPost = %v", service.ExecStartPost)
	}
	if service.ExecStop != "/app/stop" {
		t.Errorf("ExecStop = %q", service.ExecStop)
	}
	if service.Restart != RestartOnFailure {
		t.Errorf("Restart = %q", service.Restart)
	}
	if service.RestartSec != 5*time.Second {
		t.Errorf("RestartSec = %v", service.RestartSec)
	}
	if service.TimeoutStopSec != time.Minute {
		t.Errorf("TimeoutStopSec = %v", service.TimeoutStopSec)
	}
	if service.WorkingDirectory != `C:\app` {
		t.Errorf("WorkingDirectory = %q", service.WorkingDirectory)
	}
	wantEnv := map[string]string{"FOO": "bar", "BAZ": "qux", "EXTRA": "value"}
	if !reflect.DeepEqual(service.Environment, wantEnv) {
		t.Errorf("Environment = %v", service.Environment)
	}
	if !reflect.DeepEqual(service.EnvironmentFiles, []string{`C:\app\.env`, `-C:\app\.env.local`}) {
		t.Errorf("EnvironmentFiles = %v", service.EnvironmentFiles)
	}
	if service.User != `NT AUTHORITY\LocalService` {
		t.Errorf("User = %q", service.User)
	}
	if service.StandardOutput != `file:C:\logs\out.log` {
		t.Errorf("StandardOutput = %q", service.StandardOutput)
	}
	if service.StandardError != `file:C:\logs\err.log` {
		t.Errorf("StandardError = %q", service.StandardError)
	}
	if !reflect.DeepEqual(service.LogsDirectory, []string{"app-logs", "app-data"}) {
		t.Errorf("LogsDirectory = %v", service.LogsDirectory)
	}
	if service.StartLimitBurst != 3 {
		t.Errorf("StartLimitBurst = %d", service.StartLimitBurst)
	}
	if service.StartLimitIntervalSec != 30*time.Second {
		t.Errorf("StartLimitIntervalSec = %v", service.StartLimitIntervalSec)
	}
}

func TestParseTimer(t *testing.T) {
	data := []byte(`[Unit]
Description=Daily backup

[Timer]
OnCalendar=*-*-* 03:00:00
OnCalendar=daily
OnBootSec=5min
Unit=backup.service
Persistent=true
`)

	file, err := Parse(ID{Name: "backup", Type: TypeTimer}, data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if file.Service != nil {
		t.Error("Service should be nil")
	}
	if file.Description != "Daily backup" {
		t.Errorf("Description = %q", file.Description)
	}

	timer := file.Timer
	if timer == nil {
		t.Fatal("Timer is nil")
	}
	if !reflect.DeepEqual(timer.OnCalendar, []string{"*-*-* 03:00:00", "daily"}) {
		t.Errorf("OnCalendar = %v", timer.OnCalendar)
	}
	if timer.OnBootSec != 5*time.Minute {
		t.Errorf("OnBootSec = %v", timer.OnBootSec)
	}
	if timer.Unit != "backup.service" {
		t.Errorf("Unit = %q", timer.Unit)
	}
	wantWarnings := []string{"OnBootSec= is not supported", "unsupported timer directive Persistent"}
	if !reflect.DeepEqual(file.Warnings, wantWarnings) {
		t.Errorf("Warnings = %v", file.Warnings)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		id   ID
		data string
	}{
		{
			name: "missing service section",
			id:   ID{Name: "example", Type: TypeService},
			data: "[Unit]\nDescription=Example\n",
		},
		{
			name: "malformed line",
			id:   ID{Name: "example", Type: TypeService},
			data: "[Service]\nExecStart\n",
		},
		{
			name: "invalid restart policy",
			id:   ID{Name: "example", Type: TypeService},
			data: "[Service]\nRestart=sometimes\n",
		},
		{
			name: "invalid timespan",
			id:   ID{Name: "example", Type: TypeService},
			data: "[Service]\nRestartSec=soon\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse(tt.id, []byte(tt.data)); err == nil {
				t.Fatal("Parse() error = nil")
			}
		})
	}
}
