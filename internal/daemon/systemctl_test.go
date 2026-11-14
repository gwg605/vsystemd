package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/runner"
	"github.com/gwg605/vsystemd/internal/scm"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	"github.com/gwg605/vsystemd/internal/storage"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
	"github.com/gwg605/vsystemd/internal/transport"
	transportfake "github.com/gwg605/vsystemd/internal/transport/fake"
)

func newControlClient(t *testing.T, manager scm.Manager, store storage.Storage) (*Daemon, *transportfake.Transport) {
	t.Helper()
	daemon, network := newTestDaemon(t, manager, store)
	client := network.New(transport.DefaultConfig(), "systemctl-1", nil)
	t.Cleanup(func() { client.Close() })
	return daemon, client
}

func controlRequest(t *testing.T, client transport.Transport, msgType string, payload any) transport.Message {
	t.Helper()
	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s: %v", msgType, err)
		}
		raw = data
	}
	response, err := client.Req(daemonPeer, transport.Message{Type: msgType, Payload: raw})
	if err != nil {
		t.Fatalf("%s request: %v", msgType, err)
	}
	return response
}

func TestStatusResponse(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Unit]\nDescription=App\n[Service]\nExecStart=x\n"))

	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "app"}); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}
	if err := manager.StartService("app"); err != nil {
		t.Fatalf("StartService() error = %v", err)
	}

	daemon, client := newControlClient(t, manager, store)
	logDir := logwriter.ServiceLogDir(daemon.logsDir, "app")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("create log dir: %v", err)
	}
	logPath := filepath.Join(logDir, time.Now().UTC().Format(logwriter.TimestampLayout)+".log")
	if err := os.WriteFile(logPath, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	daemon.updateFromEvent(runner.StateEvent{
		Unit:         "app.service",
		State:        runner.StateActive,
		PID:          777,
		RestartCount: 2,
		Timestamp:    time.Now(),
	})

	response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "app.service"})
	if response.Type != protocol.TypeStatusResponse || response.RequestID == "" {
		t.Fatalf("response = %+v", response)
	}

	var status protocol.StatusResponse
	if err := json.Unmarshal(response.Payload, &status); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if status.Unit != "app.service" {
		t.Errorf("Unit = %q", status.Unit)
	}
	if status.SystemdState != string(SystemdActiveRunning) {
		t.Errorf("SystemdState = %q", status.SystemdState)
	}
	if status.SCMState != scm.StateRunning {
		t.Errorf("SCMState = %v", status.SCMState)
	}
	if status.PID != 777 || status.RestartCount != 2 {
		t.Errorf("status = %+v", status)
	}
	if len(status.LogTail) != 2 || status.LogTail[1] != "line2" {
		t.Errorf("LogTail = %v", status.LogTail)
	}
}

func TestStatusExplicitLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(logPath, []byte("explicit-line\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\nStandardOutput=file:"+logPath+"\n"))

	_, client := newControlClient(t, scmfake.New(), store)
	response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "app.service"})
	if response.Type != protocol.TypeStatusResponse {
		t.Fatalf("response type = %q", response.Type)
	}

	var status protocol.StatusResponse
	if err := json.Unmarshal(response.Payload, &status); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(status.LogTail) != 1 || status.LogTail[0] != "explicit-line" {
		t.Errorf("LogTail = %v", status.LogTail)
	}
}

func TestStatusUnknownUnit(t *testing.T) {
	manager := scmfake.New()
	store := storagefake.New()
	_, client := newControlClient(t, manager, store)

	response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "missing.service"})
	if response.Type != protocol.TypeErrorResponse {
		t.Fatalf("response type = %q", response.Type)
	}

	var failure protocol.ErrorResponse
	if err := json.Unmarshal(response.Payload, &failure); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if failure.Error == "" {
		t.Error("error message is empty")
	}
}

func TestListUnits(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Unit]\nDescription=App\n[Service]\nExecStart=x\n"))
	store.WriteUnit("job.timer", []byte("[Unit]\nDescription=Job\n[Timer]\nOnCalendar=daily\n"))
	lastRun := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	nextRun := time.Date(2026, 11, 9, 11, 0, 0, 0, time.UTC)
	if err := store.WriteTimerState("job.timer", storage.TimerState{
		Enabled: true,
		LastRun: lastRun,
		NextRun: nextRun,
	}); err != nil {
		t.Fatalf("WriteTimerState() error = %v", err)
	}

	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "app"}); err != nil {
		t.Fatalf("CreateService(app) error = %v", err)
	}
	if err := manager.StartService("app"); err != nil {
		t.Fatalf("StartService(app) error = %v", err)
	}
	if err := manager.CreateService(scm.ServiceConfig{Name: "orphan"}); err != nil {
		t.Fatalf("CreateService(orphan) error = %v", err)
	}

	_, client := newControlClient(t, manager, store)
	response := controlRequest(t, client, protocol.TypeListUnits, nil)
	if response.Type != protocol.TypeListUnitsResponse || response.RequestID == "" {
		t.Fatalf("response = %+v", response)
	}

	var list protocol.ListUnitsResponse
	if err := json.Unmarshal(response.Payload, &list); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	units := make(map[string]protocol.UnitStatus, len(list.Units))
	for _, entry := range list.Units {
		units[entry.Unit] = entry
	}

	app, ok := units["app.service"]
	if !ok {
		t.Fatal("app.service missing")
	}
	if app.Type != "service" || app.SystemdState != string(SystemdActiveRunning) || app.SCMState != scm.StateRunning {
		t.Errorf("app = %+v", app)
	}
	if app.Description != "App" {
		t.Errorf("app description = %q", app.Description)
	}
	if app.Enabled != nil || app.Running != nil {
		t.Errorf("app timer fields = %v/%v", app.Enabled, app.Running)
	}

	timer, ok := units["job.timer"]
	if !ok {
		t.Fatal("job.timer missing")
	}
	if timer.Type != "timer" || timer.Enabled == nil || !*timer.Enabled {
		t.Errorf("timer = %+v", timer)
	}
	if timer.Running == nil || *timer.Running {
		t.Errorf("timer running = %v", timer.Running)
	}
	if timer.LastRun == nil || !timer.LastRun.Equal(lastRun) {
		t.Errorf("timer last run = %v", timer.LastRun)
	}
	if timer.NextRun == nil || !timer.NextRun.Equal(nextRun) {
		t.Errorf("timer next run = %v", timer.NextRun)
	}

	if _, ok := units["orphan.service"]; !ok {
		t.Error("orphan.service missing")
	}
}

func TestDaemonReloadRequest(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\n"))

	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "app", BinaryPath: "old"}); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}

	_, client := newControlClient(t, manager, store)
	response := controlRequest(t, client, protocol.TypeDaemonReload, nil)
	if response.Type != protocol.TypeReloadResult || response.RequestID == "" {
		t.Fatalf("response = %+v", response)
	}

	var result protocol.ReloadResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !result.Success {
		t.Error("reload was not successful")
	}

	config, err := manager.Config("app")
	if err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if config.BinaryPath == "old" {
		t.Error("service config was not updated")
	}
}
