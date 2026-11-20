package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/protocol"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	"github.com/gwg605/vsystemd/internal/storage"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
)

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

	state, err := store.ReadTimerState("job.timer")
	if err != nil {
		t.Fatalf("ReadTimerState() error = %v", err)
	}
	if !state.Enabled {
		t.Error("enabled state was not persisted")
	}

	response = controlRequest(t, client, protocol.TypeTimerEnable, protocol.TimerRequest{Unit: "job.timer", Enabled: false})
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if result.Enabled {
		t.Error("enabled state was not cleared")
	}
	state, err = store.ReadTimerState("job.timer")
	if err != nil {
		t.Fatalf("ReadTimerState() error = %v", err)
	}
	if state.Enabled {
		t.Error("disabled state was not persisted")
	}
}

func TestTimerStartStop(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("job.timer", []byte("[Timer]\nOnCalendar=daily\n"))
	lastRun := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	if err := store.WriteTimerState("job.timer", storage.TimerState{Enabled: true, LastRun: lastRun}); err != nil {
		t.Fatalf("WriteTimerState() error = %v", err)
	}

	_, client := newControlClient(t, scmfake.New(), store)

	response := controlRequest(t, client, protocol.TypeTimerStart, protocol.TimerRequest{Unit: "job.timer"})
	var result protocol.TimerResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !result.Success || !result.Running {
		t.Errorf("result = %+v", result)
	}
	if !result.Enabled {
		t.Error("start changed enabled state")
	}
	if result.NextRun.IsZero() {
		t.Errorf("NextRun = %v, want set on start", result.NextRun)
	}
	active, err := store.ReadTimerState("job.timer")
	if err != nil {
		t.Fatalf("ReadTimerState() error = %v", err)
	}
	if active.ActiveSince.IsZero() {
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
	state, err := store.ReadTimerState("job.timer")
	if err != nil {
		t.Fatalf("ReadTimerState() error = %v", err)
	}
	if !state.Enabled {
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
	if err := store.WriteTimerState("enabled.timer", storage.TimerState{Enabled: true}); err != nil {
		t.Fatalf("WriteTimerState() error = %v", err)
	}
	if err := store.WriteTimerState("disabled.timer", storage.TimerState{Enabled: false}); err != nil {
		t.Fatalf("WriteTimerState() error = %v", err)
	}

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
	if err := store.WriteTimerState("a.timer", storage.TimerState{Enabled: true}); err != nil {
		t.Fatalf("WriteTimerState(a) error = %v", err)
	}
	if err := store.WriteTimerState("b.timer", storage.TimerState{Enabled: true}); err != nil {
		t.Fatalf("WriteTimerState(b) error = %v", err)
	}

	daemon, _ := newTestDaemon(t, scmfake.New(), store)
	if err := daemon.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !daemon.sched.Has("a.timer") || !daemon.sched.Has("b.timer") {
		t.Fatal("enabled timers were not started")
	}

	store.WriteUnit("c.timer", []byte("[Timer]\nOnCalendar=daily\n"))
	if err := store.WriteTimerState("c.timer", storage.TimerState{Enabled: true}); err != nil {
		t.Fatalf("WriteTimerState(c) error = %v", err)
	}
	store.DeleteUnit("b.timer")

	if err := daemon.reload(); err != nil {
		t.Fatalf("reload() error = %v", err)
	}

	if !daemon.sched.Has("a.timer") {
		t.Error("unchanged timer was stopped")
	}
	if daemon.sched.Has("b.timer") {
		t.Error("removed timer is still running")
	}
	if !daemon.sched.Has("c.timer") {
		t.Error("new enabled timer was not started")
	}

	if !daemon.units.keys()["b.timer"] {
		t.Error("orphan timer was dropped from the list")
	}
	state, err := store.ReadTimerState("b.timer")
	if err != nil {
		t.Fatalf("ReadTimerState(b) error = %v", err)
	}
	if !state.Enabled {
		t.Errorf("orphan timer state was cleared: %+v", state)
	}
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
	if err := store.WriteTimerState("job.timer", storage.TimerState{
		Enabled: true,
		LastRun: lastRun,
	}); err != nil {
		t.Fatalf("WriteTimerState() error = %v", err)
	}

	_, client := newControlClient(t, scmfake.New(), store)
	controlRequest(t, client, protocol.TypeTimerStart, protocol.TimerRequest{Unit: "job.timer"})

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
