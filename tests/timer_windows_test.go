//go:build windows

package tests

import (
	"encoding/json"
	"testing"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/transport"
)

func TestTimerCommands(t *testing.T) {
	env := newTestEnv()
	env.store.WriteUnit("job.timer", []byte("[Timer]\nOnCalendar=daily\n"))

	d := env.newDaemon(t)
	if err := d.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	client := env.newClient(t)

	enable := timerRequest(t, client, protocol.TypeTimerEnable, protocol.TimerRequest{Unit: "job.timer", Enabled: true})
	if !enable.Enabled || enable.Running {
		t.Errorf("enable result = %+v", enable)
	}

	start := timerRequest(t, client, protocol.TypeTimerStart, protocol.TimerRequest{Unit: "job.timer"})
	if !start.Running || !start.Enabled {
		t.Errorf("start result = %+v", start)
	}
	if start.NextRun.IsZero() {
		t.Error("next_run was not computed")
	}

	listResponse := daemonRequest(t, client, protocol.TypeListUnits, nil)
	var list protocol.ListUnitsResponse
	if err := json.Unmarshal(listResponse.Payload, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list.Units) != 1 || list.Units[0].Running == nil || !*list.Units[0].Running {
		t.Errorf("list units = %+v", list.Units)
	}

	stop := timerRequest(t, client, protocol.TypeTimerStop, protocol.TimerRequest{Unit: "job.timer"})
	if stop.Running || !stop.Enabled {
		t.Errorf("stop result = %+v", stop)
	}
	if !stop.NextRun.IsZero() {
		t.Errorf("stop result NextRun = %v, want zero", stop.NextRun)
	}

	listResponse = daemonRequest(t, client, protocol.TypeListUnits, nil)
	if err := json.Unmarshal(listResponse.Payload, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list.Units) != 1 || list.Units[0].NextRun != nil {
		t.Errorf("stopped timer list = %+v", list.Units)
	}

	disable := timerRequest(t, client, protocol.TypeTimerEnable, protocol.TimerRequest{Unit: "job.timer", Enabled: false})
	if disable.Enabled {
		t.Errorf("disable result = %+v", disable)
	}

	state, err := env.store.ReadTimerState("job.timer")
	if err != nil {
		t.Fatalf("ReadTimerState() error = %v", err)
	}
	if state.Enabled {
		t.Error("disabled state was not persisted")
	}
}

func timerRequest(t *testing.T, client transport.Transport, msgType string, request protocol.TimerRequest) protocol.TimerResult {
	t.Helper()
	response := daemonRequest(t, client, msgType, request)
	if response.Type == protocol.TypeErrorResponse {
		var failure protocol.ErrorResponse
		if err := json.Unmarshal(response.Payload, &failure); err != nil {
			t.Fatalf("unmarshal error response: %v", err)
		}
		t.Fatalf("%s: %s", msgType, failure.Error)
	}

	var result protocol.TimerResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatalf("unmarshal %s response: %v", msgType, err)
	}
	return result
}
