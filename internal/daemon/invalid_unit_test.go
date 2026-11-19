package daemon

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/scm"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	"github.com/gwg605/vsystemd/internal/storage"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
	"github.com/gwg605/vsystemd/internal/unit"
)

const brokenUnit = "[Unit]\nDescription=Broken\n"

func TestListUnitsSkipsInvalidUnit(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\n"))
	store.WriteUnit("broken.service", []byte(brokenUnit))

	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "app"}); err != nil {
		t.Fatalf("CreateService(app) error = %v", err)
	}
	if err := manager.CreateService(scm.ServiceConfig{Name: "broken"}); err != nil {
		t.Fatalf("CreateService(broken) error = %v", err)
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
	units := make(map[string]protocol.UnitStatus, len(list.Units))
	for _, entry := range list.Units {
		units[entry.Unit] = entry
	}

	app, ok := units["app.service"]
	if !ok {
		t.Fatal("app.service missing")
	}
	if app.ConfigError != "" {
		t.Errorf("app ConfigError = %q", app.ConfigError)
	}

	broken, ok := units["broken.service"]
	if !ok {
		t.Fatal("broken.service missing")
	}
	if broken.ConfigError == "" {
		t.Error("broken.service ConfigError is empty")
	}
}

func TestListUnitsInvalidTimer(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("broken.timer", []byte(brokenUnit))

	_, client := newControlClient(t, scmfake.New(), store)
	response := controlRequest(t, client, protocol.TypeListUnits, nil)
	if response.Type != protocol.TypeListUnitsResponse {
		t.Fatalf("response type = %q", response.Type)
	}

	var list protocol.ListUnitsResponse
	if err := json.Unmarshal(response.Payload, &list); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	var broken *protocol.UnitStatus
	for i := range list.Units {
		if list.Units[i].Unit == "broken.timer" {
			broken = &list.Units[i]
		}
	}
	if broken == nil {
		t.Fatal("broken.timer missing")
	}
	if broken.Type != "timer" || broken.ConfigError == "" {
		t.Errorf("broken.timer = %+v", broken)
	}
}

func TestReloadSkipsInvalidUnit(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\n"))
	store.WriteUnit("broken.service", []byte(brokenUnit))

	manager := scmfake.New()
	for _, name := range []string{"app", "broken", "removed"} {
		if err := manager.CreateService(scm.ServiceConfig{Name: name, BinaryPath: "old"}); err != nil {
			t.Fatalf("CreateService(%s) error = %v", name, err)
		}
	}

	daemon, _ := newTestDaemon(t, manager, store)
	if err := daemon.reload(); err != nil {
		t.Fatalf("reload() error = %v", err)
	}

	app, err := manager.Config("app")
	if err != nil {
		t.Fatalf("Config(app) error = %v", err)
	}
	if app.BinaryPath == "old" {
		t.Error("app was not updated")
	}
	if _, err := manager.QueryStatus("broken"); err != nil {
		t.Errorf("broken was removed: %v", err)
	}
	if _, err := manager.QueryStatus("removed"); err == nil {
		t.Error("removed still exists")
	}
	if got := daemon.configError(unit.ID{Name: "broken", Type: unit.TypeService}); got == "" {
		t.Error("broken ConfigError is empty")
	}
}

func TestReloadTimersSkipsInvalidTimer(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("broken.timer", []byte(brokenUnit))
	if err := store.WriteTimerState("broken.timer", storage.TimerState{Enabled: true}); err != nil {
		t.Fatalf("WriteTimerState() error = %v", err)
	}

	daemon, _ := newTestDaemon(t, scmfake.New(), store)
	if err := daemon.reload(); err != nil {
		t.Fatalf("reload() error = %v", err)
	}

	if _, err := store.ReadTimerState("broken.timer"); err != nil {
		t.Errorf("ReadTimerState() error = %v", err)
	}
	if got := daemon.configError(unit.ID{Name: "broken", Type: unit.TypeTimer}); got == "" {
		t.Error("broken timer ConfigError is empty")
	}
}

func TestStatusInvalidUnit(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("broken.service", []byte(brokenUnit))

	_, client := newControlClient(t, scmfake.New(), store)
	response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "broken.service"})
	if response.Type != protocol.TypeStatusResponse {
		t.Fatalf("response type = %q", response.Type)
	}

	var status protocol.StatusResponse
	if err := json.Unmarshal(response.Payload, &status); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if status.ConfigError == "" {
		t.Error("ConfigError is empty")
	}
}

func TestTimerStatusInvalidUnit(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("broken.timer", []byte(brokenUnit))

	_, client := newControlClient(t, scmfake.New(), store)
	response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "broken.timer"})
	if response.Type != protocol.TypeStatusResponse {
		t.Fatalf("response type = %q", response.Type)
	}

	var status protocol.StatusResponse
	if err := json.Unmarshal(response.Payload, &status); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if status.ConfigError == "" {
		t.Error("ConfigError is empty")
	}
}

func TestConfigErrorSurvivesHeartbeat(t *testing.T) {
	daemon, _ := newTestDaemon(t, scmfake.New(), storagefake.New())
	id := unit.ID{Name: "app", Type: unit.TypeService}
	daemon.setConfigError(id, errors.New("boom"))

	daemon.updateFromEvent(protocol.StateEvent{Unit: "app.service", State: protocol.StateActive})

	if got := daemon.configError(id); got != "boom" {
		t.Errorf("ConfigError = %q", got)
	}
}

func TestConfigErrorClearedOnValidUnit(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\n"))

	daemon, _ := newTestDaemon(t, scmfake.New(), store)
	id := unit.ID{Name: "app", Type: unit.TypeService}
	daemon.setConfigError(id, errors.New("boom"))

	if _, err := daemon.status(id); err != nil {
		t.Fatalf("status() error = %v", err)
	}
	if got := daemon.configError(id); got != "" {
		t.Errorf("ConfigError = %q", got)
	}
}
