package daemon

import (
	"encoding/json"
	"testing"

	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	"github.com/gwg605/vsystemd/internal/storage"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
)

func TestTimerEnablePersists(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("job.timer", []byte("[Timer]\nOnCalendar=daily\n"))

	_, client := newControlClient(t, scmfake.New(), store)

	response := controlRequest(t, client, messageTimerEnable, timerRequest{Unit: "job.timer", Enabled: true})
	if response.Type != messageTimerResult {
		t.Fatalf("response type = %q", response.Type)
	}

	var result timerResult
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

	response = controlRequest(t, client, messageTimerEnable, timerRequest{Unit: "job.timer", Enabled: false})
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

	_, client := newControlClient(t, scmfake.New(), store)

	response := controlRequest(t, client, messageTimerStart, timerRequest{Unit: "job.timer"})
	var result timerResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !result.Success || !result.Running {
		t.Errorf("result = %+v", result)
	}
	if result.Enabled {
		t.Error("start changed enabled state")
	}

	listResponse := controlRequest(t, client, messageListUnits, nil)
	var list listUnitsResponse
	if err := json.Unmarshal(listResponse.Payload, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list.Units) != 1 || list.Units[0].Running == nil || !*list.Units[0].Running {
		t.Errorf("list units = %+v", list.Units)
	}

	response = controlRequest(t, client, messageTimerStop, timerRequest{Unit: "job.timer"})
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if result.Running {
		t.Error("timer is still running after stop")
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

	response := controlRequest(t, client, messageListUnits, nil)
	var list listUnitsResponse
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

	response := controlRequest(t, client, messageTimerStart, timerRequest{Unit: "missing.timer"})
	if response.Type != messageErrorResponse {
		t.Fatalf("response type = %q", response.Type)
	}
}
