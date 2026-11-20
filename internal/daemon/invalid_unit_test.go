package daemon

import (
	"encoding/json"
	"testing"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/scm"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	"github.com/gwg605/vsystemd/internal/storage"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

const brokenUnit = "[Unit]\nDescription=Broken\n"

func listUnitsResponse(t *testing.T, client transport.Transport) protocol.ListUnitsResponse {
	t.Helper()
	response := controlRequest(t, client, protocol.TypeListUnits, nil)
	var list protocol.ListUnitsResponse
	if err := json.Unmarshal(response.Payload, &list); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return list
}

func indexUnits(list protocol.ListUnitsResponse) map[string]protocol.UnitStatus {
	units := make(map[string]protocol.UnitStatus, len(list.Units))
	for _, entry := range list.Units {
		units[entry.Unit] = entry
	}
	return units
}

func TestListUnitsInvalidService(t *testing.T) {
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
	units := indexUnits(listUnitsResponse(t, client))

	if app, ok := units["app.service"]; !ok || app.LoadState != string(LoadStateLoaded) {
		t.Errorf("app.service = %+v", app)
	}
	if broken, ok := units["broken.service"]; !ok || broken.LoadState != string(LoadStateBadSetting) {
		t.Errorf("broken.service = %+v", broken)
	}
}

func TestListUnitsInvalidTimer(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("broken.timer", []byte(brokenUnit))

	_, client := newControlClient(t, scmfake.New(), store)
	broken, ok := indexUnits(listUnitsResponse(t, client))["broken.timer"]
	if !ok {
		t.Fatal("broken.timer missing")
	}
	if broken.Type != "timer" || broken.LoadState != string(LoadStateBadSetting) {
		t.Errorf("broken.timer = %+v", broken)
	}
}

func TestListUnitsOrphanService(t *testing.T) {
	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "orphan"}); err != nil {
		t.Fatalf("CreateService(orphan) error = %v", err)
	}

	_, client := newControlClient(t, manager, storagefake.New())
	orphan, ok := indexUnits(listUnitsResponse(t, client))["orphan.service"]
	if !ok {
		t.Fatal("orphan.service missing")
	}
	if orphan.LoadState != string(LoadStateNotFound) {
		t.Errorf("orphan LoadState = %q", orphan.LoadState)
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
	if entry, _ := daemon.units.entry(unit.ID{Name: "broken", Type: unit.TypeService}); entry.loadState != LoadStateBadSetting {
		t.Errorf("broken LoadState = %q", entry.loadState)
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
	if entry, _ := daemon.units.entry(unit.ID{Name: "broken", Type: unit.TypeTimer}); entry.loadState != LoadStateBadSetting {
		t.Errorf("broken timer LoadState = %q", entry.loadState)
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
	if status.LoadState != string(LoadStateBadSetting) || status.LoadError == "" {
		t.Errorf("status = %+v", status)
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
	if status.LoadState != string(LoadStateBadSetting) || status.LoadError == "" {
		t.Errorf("status = %+v", status)
	}
}

func TestVerifyUnitFile(t *testing.T) {
	store := storagefake.New()
	content := []byte("[Service]\nExecStart=x\n")
	store.WriteUnit("app.service", content)
	store.WriteUnit("broken.service", []byte(brokenUnit))

	daemon, _ := newTestDaemon(t, scmfake.New(), store)
	app := unit.ID{Name: "app", Type: unit.TypeService}

	if err := daemon.verifyUnitFile(app, content); err != nil {
		t.Errorf("verifyUnitFile(app) error = %v", err)
	}
	if err := daemon.verifyUnitFile(app, []byte("changed")); err == nil {
		t.Error("verifyUnitFile() error = nil for changed content")
	}
	if err := daemon.verifyUnitFile(unit.ID{Name: "missing", Type: unit.TypeService}, content); err == nil {
		t.Error("verifyUnitFile() error = nil for unknown unit")
	}
	if err := daemon.verifyUnitFile(unit.ID{Name: "broken", Type: unit.TypeService}, []byte(brokenUnit)); err == nil {
		t.Error("verifyUnitFile() error = nil for bad-setting unit")
	}
}

func TestStatusForgottenAfterReload(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("broken.service", []byte(brokenUnit))

	daemon, client := newControlClient(t, scmfake.New(), store)
	if response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "broken.service"}); response.Type != protocol.TypeStatusResponse {
		t.Fatalf("response type = %q", response.Type)
	}

	store.DeleteUnit("broken.service")
	if err := daemon.reload(); err != nil {
		t.Fatalf("reload() error = %v", err)
	}

	response := controlRequest(t, client, protocol.TypeStatusRequest, protocol.StatusRequest{Unit: "broken.service"})
	if response.Type != protocol.TypeErrorResponse {
		t.Fatalf("response type = %q", response.Type)
	}
	if _, ok := daemon.units.entry(unit.ID{Name: "broken", Type: unit.TypeService}); ok {
		t.Error("unit was not dropped from the list")
	}
}

func TestReloadPrunesRuntimeState(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\n"))

	daemon, _ := newTestDaemon(t, scmfake.New(), store)
	daemon.updateFromEvent(protocol.StateEvent{Unit: "app.service", State: protocol.StateActive})
	if _, ok := daemon.State("app.service"); !ok {
		t.Fatal("state was not recorded")
	}

	store.DeleteUnit("app.service")
	if err := daemon.reload(); err != nil {
		t.Fatalf("reload() error = %v", err)
	}
	if _, ok := daemon.State("app.service"); ok {
		t.Error("state was not pruned")
	}
}
