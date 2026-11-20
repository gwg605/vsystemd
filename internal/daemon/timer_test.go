package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/protocol"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
)

// timerRecord mirrors the on-disk timer database layout so tests can seed it
// before a daemon is created.
type timerRecord struct {
	OnCalendar  []string  `json:"on_calendar"`
	Enabled     bool      `json:"enabled"`
	LastRun     time.Time `json:"last_run"`
	NextRun     time.Time `json:"next_run"`
	ActiveSince time.Time `json:"active_since"`
}

func writeTimerDB(t *testing.T, store *storagefake.Storage, records map[string]timerRecord) {
	t.Helper()
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("marshal timer db: %v", err)
	}
	if err := store.WriteTimers(data); err != nil {
		t.Fatalf("WriteTimers() error = %v", err)
	}
}

func readTimerDB(t *testing.T, store *storagefake.Storage, name string) timerRecord {
	t.Helper()
	data, err := store.ReadTimers()
	if err != nil {
		t.Fatalf("ReadTimers() error = %v", err)
	}
	records := make(map[string]timerRecord)
	if len(data) > 0 {
		if err := json.Unmarshal(data, &records); err != nil {
			t.Fatalf("unmarshal timer db: %v", err)
		}
	}
	return records[name]
}

func TestTimerEnablePersists(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("job.timer", []byte("[Timer]\nOnCalendar=daily\n"))

	_, client := newControlClient(t, scmfake.New(), store)

	response := controlRequest(t, client, protocol.TypeTimerEnable, protocol.TimerRequest{Unit: "job.timer", Enabled: true})
	if response.Type != protocol.TypeTimerResult {
		t.Fatalf("response type = %q", response.Type)
	}

	var result protocol.TimerResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !result.Success || !result.Enabled || result.Running {
		t.Errorf("result = %+v", result)
	}

	if record := readTimerDB(t, store, "job.timer"); !record.Enabled {
		t.Error("enabled state was not persisted")
	}

	response = controlRequest(t, client, protocol.TypeTimerEnable, protocol.TimerRequest{Unit: "job.timer", Enabled: false})
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if result.Enabled {
		t.Error("enabled state was not cleared")
	}
	if record := readTimerDB(t, store, "job.timer"); record.Enabled {
		t.Error("disabled state was not persisted")
	}
}

func TestTimerStartStop(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("job.timer", []byte("[Timer]\nOnCalendar=daily\n"))
	lastRun := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	writeTimerDB(t, store, map[string]timerRecord{
		"job.timer": {OnCalendar: []string{"daily"}, Enabled: false, LastRun: lastRun},
	})

	_, client := newControlClient(t, scmfake.New(), store)

	response := controlRequest(t, client, protocol.TypeTimerStart, protocol.TimerRequest{Unit: "job.timer"})
	var result protocol.TimerResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !result.Success || !result.Running {
		t.Errorf("result = %+v", result)
	}
	if result.Enabled {
		t.Error("start changed enabled state")
	}
	if result.NextRun.IsZero() {
		t.Errorf("NextRun = %v, want set on start", result.NextRun)
	}
	if active := readTimerDB(t, store, "job.timer"); active.ActiveSince.IsZero() {
		t.Error("ActiveSince was not set on start")
	}

	listResponse := controlRequest(t, client, protocol.TypeListUnits, nil)
	var list protocol.ListUnitsResponse
	if err := json.Unmarshal(listResponse.Payload, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list.Units) != 1 || list.Units[0].Running == nil || !*list.Units[0].Running {
		t.Errorf("list units = %+v", list.Units)
	}

	response = controlRequest(t, client, protocol.TypeTimerStop, protocol.TimerRequest{Unit: "job.timer"})
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if result.Running {
		t.Error("timer is still running after stop")
	}
	state := readTimerDB(t, store, "job.timer")
	if state.Enabled {
		t.Error("stop changed enabled state")
	}
	if !state.NextRun.IsZero() {
		t.Errorf("NextRun = %v, want zero after stop", state.NextRun)
	}
	if !state.ActiveSince.IsZero() {
		t.Errorf("ActiveSince = %v, want zero after stop", state.ActiveSince)
	}
	if !state.LastRun.Equal(lastRun) {
		t.Errorf("LastRun = %v, want %v", state.LastRun, lastRun)
	}
}

func TestStartAutoStartsEnabledTimers(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("enabled.timer", []byte("[Timer]\nOnCalendar=daily\n"))
	store.WriteUnit("disabled.timer", []byte("[Timer]\nOnCalendar=daily\n"))
	writeTimerDB(t, store, map[string]timerRecord{
		"enabled.timer":  {OnCalendar: []string{"daily"}, Enabled: true},
		"disabled.timer": {OnCalendar: []string{"daily"}, Enabled: false},
	})

	daemon, client := newControlClient(t, scmfake.New(), store)
	if err := daemon.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	response := controlRequest(t, client, protocol.TypeListUnits, nil)
	var list protocol.ListUnitsResponse
	if err := json.Unmarshal(response.Payload, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}

	running := make(map[string]bool, len(list.Units))
	for _, entry := range list.Units {
		if entry.Running != nil {
			running[entry.Unit] = *entry.Running
		}
	}
	if !running["enabled.timer"] {
		t.Error("enabled timer was not started")
	}
	if running["disabled.timer"] {
		t.Error("disabled timer was started")
	}
}

func TestTimerUnknown(t *testing.T) {
	store := storagefake.New()
	_, client := newControlClient(t, scmfake.New(), store)

	response := controlRequest(t, client, protocol.TypeTimerStart, protocol.TimerRequest{Unit: "missing.timer"})
	if response.Type != protocol.TypeErrorResponse {
		t.Fatalf("response type = %q", response.Type)
	}
}

func TestReloadTimers(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("a.timer", []byte("[Timer]\nOnCalendar=daily\n"))
	store.WriteUnit("b.timer", []byte("[Timer]\nOnCalendar=daily\n"))
	writeTimerDB(t, store, map[string]timerRecord{
		"a.timer": {OnCalendar: []string{"daily"}, Enabled: true},
		"b.timer": {OnCalendar: []string{"daily"}, Enabled: true},
	})

	daemon, _ := newTestDaemon(t, scmfake.New(), store)
	if err := daemon.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !timerRunning(t, daemon, "a.timer") || !timerRunning(t, daemon, "b.timer") {
		t.Fatal("enabled timers were not started")
	}

	store.WriteUnit("c.timer", []byte("[Timer]\nOnCalendar=daily\n"))
	store.DeleteUnit("b.timer")

	if err := daemon.reload(); err != nil {
		t.Fatalf("reload() error = %v", err)
	}

	if !timerRunning(t, daemon, "a.timer") {
		t.Error("unchanged timer was stopped")
	}
	if !daemon.units.keys()["b.timer"] {
		t.Error("orphan timer was dropped from the list")
	}
	if status := readTimerDB(t, store, "b.timer"); !status.Enabled {
		t.Errorf("orphan timer state was cleared: %+v", status)
	}
	if !daemon.units.keys()["c.timer"] {
		t.Error("new timer was not added to the list")
	}

	// A removed timer that is disabled and stopped is deleted.
	if err := daemon.timers.StopTimer("b.timer"); err != nil {
		t.Fatalf("StopTimer(b) error = %v", err)
	}
	if err := daemon.timers.Enable("b.timer", false); err != nil {
		t.Fatalf("Enable(b) error = %v", err)
	}
	if err := daemon.reload(); err != nil {
		t.Fatalf("reload() error = %v", err)
	}
	if _, err := daemon.timers.QueryStatus("b.timer"); err == nil {
		t.Error("disabled, stopped timer without unit file was not removed")
	}
}

func timerRunning(t *testing.T, d *Daemon, name string) bool {
	t.Helper()
	status, err := d.timers.QueryStatus(name)
	if err != nil {
		t.Fatalf("QueryStatus(%s) error = %v", name, err)
	}
	return status.Running
}

func TestTimerStatus(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "backup.log")
	if err := os.WriteFile(logPath, []byte("backup-line\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	store := storagefake.New()
	store.WriteUnit("job.timer", []byte("[Unit]\nDescription=Daily backup\n[Timer]\nOnCalendar=daily\nUnit=backup.service\n"))
	store.WriteUnit("backup.service", []byte("[Service]\nExecStart=x\nStandardOutput=file:"+logPath+"\n"))
	lastRun := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	writeTimerDB(t, store, map[string]timerRecord{
		"job.timer": {OnCalendar: []string{"daily"}, Enabled: true, LastRun: lastRun},
	})

	daemon, client := newControlClient(t, scmfake.New(), store)
	if err := daemon.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "job.timer"})
	if response.Type != protocol.TypeStatusResponse {
		t.Fatalf("response type = %q", response.Type)
	}

	var status protocol.StatusResponse
	if err := json.Unmarshal(response.Payload, &status); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if status.SystemdState != string(SystemdActiveWaiting) {
		t.Errorf("SystemdState = %q", status.SystemdState)
	}
	if status.Description != "Daily backup" {
		t.Errorf("Description = %q", status.Description)
	}
	if status.Enabled == nil || !*status.Enabled {
		t.Errorf("Enabled = %v", status.Enabled)
	}
	if status.Running == nil || !*status.Running {
		t.Errorf("Running = %v", status.Running)
	}
	if status.Activates != "backup.service" {
		t.Errorf("Activates = %q", status.Activates)
	}
	if status.ActiveSince == nil || status.ActiveSince.IsZero() {
		t.Errorf("ActiveSince = %v", status.ActiveSince)
	}
	if status.LastRun == nil || !status.LastRun.Equal(lastRun) {
		t.Errorf("LastRun = %v", status.LastRun)
	}
	if status.NextRun == nil || status.NextRun.IsZero() {
		t.Errorf("NextRun = %v", status.NextRun)
	}
	if len(status.LogTail) != 1 || status.LogTail[0] != "backup-line" {
		t.Errorf("LogTail = %v", status.LogTail)
	}
}
