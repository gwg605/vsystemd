//go:build windows

package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/scm"
)

func TestDaemonReload(t *testing.T) {
	env := newTestEnv()
	env.store.WriteUnit("app.service", []byte("[Unit]\nDescription=App\n[Service]\nExecStart=app\n"))
	env.store.WriteUnit("new.service", []byte("[Service]\nExecStart=new\n"))

	if err := env.manager.CreateService(scm.ServiceConfig{Name: "app", BinaryPath: "old"}); err != nil {
		t.Fatalf("CreateService(app) error = %v", err)
	}
	if err := env.manager.CreateService(scm.ServiceConfig{Name: "removed", BinaryPath: "old", StartType: scm.StartTypeDisabled}); err != nil {
		t.Fatalf("CreateService(removed) error = %v", err)
	}

	d := env.newDaemon(t)
	if err := d.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	client := env.newClient(t)
	response := daemonRequest(t, client, protocol.TypeDaemonReload, nil)
	if response.Type != protocol.TypeReloadResult {
		t.Fatalf("response type = %q", response.Type)
	}

	var result protocol.ReloadResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !result.Success {
		t.Error("reload was not successful")
	}

	config, err := env.manager.Config("app")
	if err != nil {
		t.Fatalf("Config(app) error = %v", err)
	}
	if !strings.Contains(config.BinaryPath, "--mode=run") {
		t.Errorf("BinaryPath = %q", config.BinaryPath)
	}
	if _, err := env.manager.QueryStatus("removed"); err == nil {
		t.Error("removed service still exists")
	}
	if _, err := env.manager.QueryStatus("new"); err == nil {
		t.Error("new service was created")
	}
}
