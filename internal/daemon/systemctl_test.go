package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/scm"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	"github.com/gwg605/vsystemd/internal/storage"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
	"github.com/gwg605/vsystemd/internal/transport"
	transportfake "github.com/gwg605/vsystemd/internal/transport/fake"
	"github.com/gwg605/vsystemd/internal/unit"
	"github.com/gwg605/vsystemd/internal/unitpath"
)

func newControlClient(t *testing.T, manager scm.Manager, store storage.Storage) (*Daemon, *transportfake.Transport) {
	t.Helper()
	daemon, network := newTestDaemon(t, manager, store)
	client := network.New(transport.DefaultConfig(), transport.NewPeer(transport.PeerControl, "systemctl_1", true), nil)
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
	response, err := client.Req(client.Target(transport.PeerDaemon), transport.Message{Type: msgType, Payload: raw})
	if err != nil {
		t.Fatalf("%s request: %v", msgType, err)
	}
	return response
}

func TestStatusResponse(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Unit]\nDescription=App\n[Service]\nExecStart=x\n"))

	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "app", StartType: scm.StartTypeAutomatic}, nil); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}
	if err := manager.StartService("app"); err != nil {
		t.Fatalf("StartService() error = %v", err)
	}

	daemon, client := newControlClient(t, manager, store)
	logDir := logwriter.ServiceLogDir(daemon.roots.LogsDir, "app")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("create log dir: %v", err)
	}
	logPath := filepath.Join(logDir, time.Now().UTC().Format(logwriter.TimestampLayout)+".log")
	if err := os.WriteFile(logPath, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	daemon.updateFromEvent(protocol.StateEvent{
		Unit:         "app.service",
		State:        protocol.StateActive,
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
	if status.Description != "App" {
		t.Errorf("Description = %q", status.Description)
	}
	if status.Enabled == nil || !*status.Enabled {
		t.Errorf("Enabled = %v", status.Enabled)
	}
	if len(status.LogTail) != 2 || status.LogTail[1] != "line2" {
		t.Errorf("LogTail = %v", status.LogTail)
	}
}

func TestStatusIgnoresSCMPID(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\n"))

	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "app"}, nil); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}
	if err := manager.StartService("app"); err != nil {
		t.Fatalf("StartService() error = %v", err)
	}

	_, client := newControlClient(t, manager, store)
	response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "app.service"})
	if response.Type != protocol.TypeStatusResponse {
		t.Fatalf("response type = %q", response.Type)
	}

	var status protocol.StatusResponse
	if err := json.Unmarshal(response.Payload, &status); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if status.SCMState != scm.StateRunning {
		t.Errorf("SCMState = %v", status.SCMState)
	}
	if status.PID != 0 {
		t.Errorf("PID = %d, want 0 (no runner event)", status.PID)
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

func TestStatusSpecifierLog(t *testing.T) {
	logsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(logsDir, "app.log"), []byte("spec-line\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\nStandardOutput=append:%L\\app.log\n"))

	_, network := newTestDaemonRoots(t, scmfake.New(), store, unitpath.Roots{LogsDir: logsDir})
	client := network.New(transport.DefaultConfig(), transport.NewPeer(transport.PeerControl, "systemctl_1", true), nil)
	t.Cleanup(func() { client.Close() })

	response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "app.service"})
	if response.Type != protocol.TypeStatusResponse {
		t.Fatalf("response type = %q", response.Type)
	}

	var status protocol.StatusResponse
	if err := json.Unmarshal(response.Payload, &status); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(status.LogTail) != 1 || status.LogTail[0] != "spec-line" {
		t.Errorf("LogTail = %v", status.LogTail)
	}
}

func TestStatusNeedsRestart(t *testing.T) {
	unitText := []byte("[Service]\nExecStart=x\n")
	store := storagefake.New()
	store.WriteUnit("app.service", unitText)

	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "app"}, nil); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}
	if err := manager.StartService("app"); err != nil {
		t.Fatalf("StartService() error = %v", err)
	}

	daemon, client := newControlClient(t, manager, store)

	daemon.updateFromEvent(protocol.StateEvent{Unit: "app.service", State: protocol.StateActive, PID: 42, LoadedHash: "deadbeef"})
	status := statusResponse(t, client)
	if !status.NeedsRestart {
		t.Error("NeedsRestart = false, want true")
	}

	daemon.updateFromEvent(protocol.StateEvent{Unit: "app.service", State: protocol.StateActive, PID: 42, LoadedHash: unit.Content(unitText).Hash()})
	status = statusResponse(t, client)
	if status.NeedsRestart {
		t.Error("NeedsRestart = true, want false")
	}
}

func statusResponse(t *testing.T, client transport.Transport) protocol.StatusResponse {
	t.Helper()
	response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "app.service"})
	if response.Type != protocol.TypeStatusResponse {
		t.Fatalf("response type = %q", response.Type)
	}
	var status protocol.StatusResponse
	if err := json.Unmarshal(response.Payload, &status); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return status
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
	writeTimerDB(t, store, map[string]timerRecord{
		"job.timer": {OnCalendar: []string{"daily"}, Enabled: true, LastRun: lastRun},
	})

	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "app"}, nil); err != nil {
		t.Fatalf("CreateService(app) error = %v", err)
	}
	if err := manager.StartService("app"); err != nil {
		t.Fatalf("StartService(app) error = %v", err)
	}
	if err := manager.CreateService(scm.ServiceConfig{Name: "orphan"}, nil); err != nil {
		t.Fatalf("CreateService(orphan) error = %v", err)
	}

	daemon, client := newControlClient(t, manager, store)
	if err := daemon.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
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
	if timer.Running == nil || !*timer.Running {
		t.Errorf("timer running = %v", timer.Running)
	}
	if timer.LastRun == nil || !timer.LastRun.Equal(lastRun) {
		t.Errorf("timer last run = %v", timer.LastRun)
	}
	if timer.NextRun == nil || timer.NextRun.IsZero() {
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
	if err := manager.CreateService(scm.ServiceConfig{Name: "app", BinaryPath: "old"}, nil); err != nil {
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
	if config.BinaryPath != "old" {
		t.Errorf("reload changed service config = %q", config.BinaryPath)
	}
}

func TestStatusRequestInvalidUnit(t *testing.T) {
	_, client := newControlClient(t, scmfake.New(), storagefake.New())
	response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "../evil.service"})
	if response.Type != protocol.TypeErrorResponse {
		t.Fatalf("response type = %q, want error", response.Type)
	}
}

func TestListUnitsExcludesManagerService(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\n"))

	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "app"}, nil); err != nil {
		t.Fatalf("CreateService(app) error = %v", err)
	}
	if err := manager.CreateService(scm.ServiceConfig{Name: config.ManagerServiceName}, nil); err != nil {
		t.Fatalf("CreateService(manager) error = %v", err)
	}

	_, client := newControlClient(t, manager, store)
	response := controlRequest(t, client, protocol.TypeListUnits, nil)
	if response.Type != protocol.TypeListUnitsResponse {
		t.Fatalf("response type = %q", response.Type)
	}

	var list protocol.ListUnitsResponse
	if err := json.Unmarshal(response.Payload, &list); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	for _, entry := range list.Units {
		if entry.Unit == config.ManagerServiceName+".service" {
			t.Error("manager service is listed")
		}
	}
}
