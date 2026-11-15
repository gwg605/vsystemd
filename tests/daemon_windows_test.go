//go:build windows

package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gwg605/vsystemd/internal/daemon"
	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/runner"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/transport"
	transportfake "github.com/gwg605/vsystemd/internal/transport/fake"
)

func TestDaemonReload(t *testing.T) {
	env := newTestEnv()
	env.store.WriteUnit("app.service", []byte("[Unit]\nDescription=App\n[Service]\nExecStart=app\n"))
	env.store.WriteUnit("new.service", []byte("[Service]\nExecStart=new\n"))

	if err := env.manager.CreateService(scm.ServiceConfig{Name: "app", BinaryPath: "old"}); err != nil {
		t.Fatalf("CreateService(app) error = %v", err)
	}
	if err := env.manager.CreateService(scm.ServiceConfig{Name: "removed", BinaryPath: "old"}); err != nil {
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

func TestDaemonRestartStateRecovery(t *testing.T) {
	env := newTestEnv()
	env.store.WriteUnit("app.service", []byte("[Service]\nExecStart=app\n"))

	if err := env.manager.CreateService(scm.ServiceConfig{Name: "app"}); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}
	if err := env.manager.StartService("app"); err != nil {
		t.Fatalf("StartService() error = %v", err)
	}

	var runnerTransport *transportfake.Transport
	runnerTransport = env.network.New(transport.DefaultConfig(), transport.NewPeer(transport.PeerRunner, "app.service", false), func(source transport.Peer, msg transport.Message) error {
		if msg.Type != runner.MessageStatusRequest {
			return nil
		}
		payload, err := json.Marshal(runner.StateEvent{
			Unit:         "app.service",
			State:        runner.StateActive,
			PID:          555,
			RestartCount: 2,
		})
		if err != nil {
			return err
		}
		return runnerTransport.Send(source, transport.Message{
			Type:      runner.MessageStateEvent,
			RequestID: msg.ID,
			Payload:   payload,
		})
	})
	t.Cleanup(func() { runnerTransport.Close() })

	first := env.newDaemon(t)
	if err := first.Start(); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	assertRecoveredState(t, first)

	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second := env.newDaemon(t)
	if err := second.Start(); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	assertRecoveredState(t, second)
}

func assertRecoveredState(t *testing.T, d *daemon.Daemon) {
	t.Helper()
	state, ok := d.State("app.service")
	if !ok {
		t.Fatal("state was not recovered")
	}
	if state.RunnerState != daemon.RunnerStateActive {
		t.Errorf("RunnerState = %q", state.RunnerState)
	}
	if state.SCMState != scm.StateRunning {
		t.Errorf("SCMState = %v", state.SCMState)
	}
	if state.PID != 555 || state.RestartCount != 2 {
		t.Errorf("state = %+v", state)
	}
}
