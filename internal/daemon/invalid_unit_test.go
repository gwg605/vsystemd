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
	if app.LoadState != string(LoadStateLoaded) {
		t.Errorf("app LoadState = %q", app.LoadState)
	}

	broken, ok := units["broken.service"]
	if !ok {
		t.Fatal("broken.service missing")
	}
	if broken.LoadState != string(LoadStateBadSetting) {
		t.Errorf("broken.service LoadState = %q", broken.LoadState)
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
	if broken.Type != "timer" || broken.LoadState != string(LoadStateBadSetting) {
		t.Errorf("broken.timer = %+v", broken)
	}
}

func TestReloadSkipsInvalidUnit(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\n"))
	store.WriteUnit("broken.service", []byte(brokenUnit))

	manager := scmfake.New()
	for _, name := range []string{"app", "broken"} {
		if err := manager.CreateService(scm.ServiceConfig{Name: name, BinaryPath: "old"}); err != nil {
			t.Fatalf("CreateService(%s) error = %v", name, err)
		}
	}
	if err := manager.CreateService(scm.ServiceConfig{Name: "removed", BinaryPath: "old", StartType: scm.StartTypeDisabled}); err != nil {
		t.Fatalf("CreateService(removed) error = %v", err)
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
	if got, _ := daemon.loadState(unit.ID{Name: "broken", Type: unit.TypeService}); got != LoadStateBadSetting {
		t.Errorf("broken LoadState = %q", got)
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
	if got, _ := daemon.loadState(unit.ID{Name: "broken", Type: unit.TypeTimer}); got != LoadStateBadSetting {
		t.Errorf("broken timer LoadState = %q", got)
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
	if status.LoadState != string(LoadStateBadSetting) {
		t.Errorf("LoadState = %q", status.LoadState)
	}
	if status.LoadError == "" {
		t.Error("LoadError is empty")
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
	if status.LoadState != string(LoadStateBadSetting) {
		t.Errorf("LoadState = %q", status.LoadState)
	}
	if status.LoadError == "" {
		t.Error("LoadError is empty")
	}
}

func TestLoadErrorSurvivesHeartbeat(t *testing.T) {
	daemon, _ := newTestDaemon(t, scmfake.New(), storagefake.New())
	id := unit.ID{Name: "app", Type: unit.TypeService}
	daemon.setLoadError(id, LoadStateBadSetting, errors.New("boom"))

	daemon.updateFromEvent(protocol.StateEvent{Unit: "app.service", State: protocol.StateActive})

	if state, msg := daemon.loadState(id); state != LoadStateBadSetting || msg != "boom" {
		t.Errorf("load state = %q, %q", state, msg)
	}
}

func TestLoadErrorClearedOnValidUnit(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\n"))

	daemon, _ := newTestDaemon(t, scmfake.New(), store)
	id := unit.ID{Name: "app", Type: unit.TypeService}
	daemon.setLoadError(id, LoadStateBadSetting, errors.New("boom"))

	if _, err := daemon.status(id); err != nil {
		t.Fatalf("status() error = %v", err)
	}
	if state, msg := daemon.loadState(id); state != LoadStateLoaded || msg != "" {
		t.Errorf("load state = %q, %q", state, msg)
	}
}

func TestStatusForgottenAfterUnitRemoved(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("broken.service", []byte(brokenUnit))

	daemon, client := newControlClient(t, scmfake.New(), store)
	controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "broken.service"})
	if _, ok := daemon.State("broken.service"); !ok {
		t.Fatal("state was not recorded")
	}

	store.DeleteUnit("broken.service")
	response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "broken.service"})
	if response.Type != protocol.TypeErrorResponse {
		t.Fatalf("response type = %q", response.Type)
	}
	if _, ok := daemon.State("broken.service"); ok {
		t.Error("state was not forgotten")
	}
}

func TestTimerStatusForgottenAfterUnitRemoved(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("broken.timer", []byte(brokenUnit))

	daemon, client := newControlClient(t, scmfake.New(), store)
	controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "broken.timer"})
	if _, ok := daemon.State("broken.timer"); !ok {
		t.Fatal("state was not recorded")
	}

	store.DeleteUnit("broken.timer")
	response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "broken.timer"})
	if response.Type != protocol.TypeErrorResponse {
		t.Fatalf("response type = %q", response.Type)
	}
	if _, ok := daemon.State("broken.timer"); ok {
		t.Error("state was not forgotten")
	}
}

func TestListUnitsPrunesMissingState(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("broken.service", []byte(brokenUnit))

	daemon, client := newControlClient(t, scmfake.New(), store)
	controlRequest(t, client, protocol.TypeListUnits, nil)
	if _, ok := daemon.State("broken.service"); !ok {
		t.Fatal("state was not recorded")
	}

	store.DeleteUnit("broken.service")
	controlRequest(t, client, protocol.TypeListUnits, nil)
	if _, ok := daemon.State("broken.service"); ok {
		t.Error("state was not pruned")
	}
}

func TestReloadPrunesMissingState(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("broken.service", []byte(brokenUnit))

	daemon, _ := newTestDaemon(t, scmfake.New(), store)
	if _, err := daemon.status(unit.ID{Name: "broken", Type: unit.TypeService}); err != nil {
		t.Fatalf("status() error = %v", err)
	}
	if _, ok := daemon.State("broken.service"); !ok {
		t.Fatal("state was not recorded")
	}

	store.DeleteUnit("broken.service")
	if err := daemon.reload(); err != nil {
		t.Fatalf("reload() error = %v", err)
	}
	if _, ok := daemon.State("broken.service"); ok {
		t.Error("state was not pruned by reload")
	}
}
