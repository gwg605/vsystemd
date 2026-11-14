package daemon

import (
	"encoding/json"
	"testing"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/runner"
	"github.com/gwg605/vsystemd/internal/scm"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	"github.com/gwg605/vsystemd/internal/storage"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
	"github.com/gwg605/vsystemd/internal/transport"
	transportfake "github.com/gwg605/vsystemd/internal/transport/fake"
)

func newTestDaemon(t *testing.T, manager scm.Manager, store storage.Storage) (*Daemon, *transportfake.Network) {
	t.Helper()

	network := transportfake.NewNetwork()
	daemon, err := New(manager, store, `C:\vsystemd\vsystemd.exe`, t.TempDir(), logwriter.Rotation{}, func(peer string, handler transport.MsgHandler) (transport.Transport, error) {
		return network.New(transport.DefaultConfig(), peer, handler), nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { daemon.Close() })
	return daemon, network
}

func TestHandleStateEvent(t *testing.T) {
	store := storagefake.New()
	manager := scmfake.New()
	if err := manager.CreateService(scm.ServiceConfig{Name: "app"}); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}
	if err := manager.StartService("app"); err != nil {
		t.Fatalf("StartService() error = %v", err)
	}

	daemon, network := newTestDaemon(t, manager, store)
	runnerTransport := network.New(transport.DefaultConfig(), "app.service", nil)

	payload, err := json.Marshal(runner.StateEvent{
		Unit:         "app.service",
		State:        runner.StateActive,
		PID:          321,
		RestartCount: 2,
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if err := runnerTransport.Send(daemonPeer, transport.Message{
		Type:    runner.MessageStateEvent,
		Payload: payload,
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	state, ok := daemon.State("app.service")
	if !ok {
		t.Fatal("state not found")
	}
	if state.SCMState != scm.StateRunning {
		t.Errorf("SCMState = %v", state.SCMState)
	}
	if state.RunnerState != RunnerStateActive {
		t.Errorf("RunnerState = %q", state.RunnerState)
	}
	if state.PID != 321 || state.RestartCount != 2 {
		t.Errorf("state = %+v", state)
	}
	if state.LastTransition.IsZero() {
		t.Error("LastTransition is zero")
	}
	if got := state.Status(); got != SystemdActiveRunning {
		t.Errorf("Status() = %q", got)
	}
}

func TestRequestRunnerStates(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\n"))
	manager := scmfake.New()

	daemon, network := newTestDaemon(t, manager, store)

	var runnerTransport *transportfake.Transport
	runnerTransport = network.New(transport.DefaultConfig(), "app.service", func(source string, msg transport.Message) error {
		if msg.Type != runner.MessageStatusRequest {
			return nil
		}
		payload, err := json.Marshal(runner.StateEvent{
			Unit:  "app.service",
			State: runner.StateActive,
			PID:   99,
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

	if err := daemon.requestRunnerStates(); err != nil {
		t.Fatalf("requestRunnerStates() error = %v", err)
	}

	state, ok := daemon.State("app.service")
	if !ok {
		t.Fatal("state not found")
	}
	if state.RunnerState != RunnerStateActive || state.PID != 99 {
		t.Errorf("state = %+v", state)
	}
}
