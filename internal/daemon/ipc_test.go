package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/scm"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	"github.com/gwg605/vsystemd/internal/storage"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
	"github.com/gwg605/vsystemd/internal/transport"
	transportfake "github.com/gwg605/vsystemd/internal/transport/fake"
	"github.com/gwg605/vsystemd/internal/unitpath"
)

func newTestDaemon(t *testing.T, manager scm.Manager, store storage.Storage) (*Daemon, *transportfake.Network) {
	t.Helper()
	return newTestDaemonRoots(t, manager, store, unitpath.Roots{LogsDir: t.TempDir()})
}

func newTestDaemonRoots(t *testing.T, manager scm.Manager, store storage.Storage, roots unitpath.Roots) (*Daemon, *transportfake.Network) {
	t.Helper()

	network := transportfake.NewNetwork()
	daemon, err := New(manager, store, `C:\vsystemd\vsystemd.exe`, roots, logwriter.Rotation{}, func(local transport.Peer, handler transport.MsgHandler) (transport.Transport, error) {
		return network.New(transport.DefaultConfig(), local, handler), nil
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
	if err := manager.CreateService(scm.ServiceConfig{Name: "app"}, nil); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}
	if err := manager.StartService("app"); err != nil {
		t.Fatalf("StartService() error = %v", err)
	}

	daemon, network := newTestDaemon(t, manager, store)
	runnerTransport := network.New(transport.DefaultConfig(), transport.NewPeer(transport.PeerRunner, "app.service", false), nil)

	payload, err := json.Marshal(protocol.StateEvent{
		Unit:         "app.service",
		State:        protocol.StateActive,
		PID:          321,
		RestartCount: 2,
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if err := runnerTransport.Send(runnerTransport.Target(transport.PeerDaemon), transport.Message{
		Type:    protocol.MessageStateEvent,
		Payload: payload,
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	state, ok := daemon.State("app.service")
	if !ok {
		t.Fatal("state not found")
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
	if got := state.Status(scm.StateRunning); got != SystemdActiveRunning {
		t.Errorf("Status() = %q", got)
	}
}

func sendRunnerEvent(t *testing.T, network *transportfake.Network, user string, event protocol.StateEvent) error {
	t.Helper()
	runnerTransport := network.New(
		transport.DefaultConfig(),
		transport.NewPeer(transport.PeerRunner, "app.service", false).WithService(scm.ServiceConfig{User: scm.Account(user)}),
		nil,
	)
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return runnerTransport.Send(runnerTransport.Target(transport.PeerDaemon), transport.Message{
		Type:    protocol.MessageStateEvent,
		Payload: payload,
	})
}

func TestStateEventAccountMatch(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\nUser=NT SERVICE\\app\n"))

	daemon, network := newTestDaemon(t, scmfake.New(), store)
	err := sendRunnerEvent(t, network, `NT SERVICE\app`, protocol.StateEvent{
		Unit:  "app.service",
		State: protocol.StateActive,
		PID:   7,
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if state, ok := daemon.State("app.service"); !ok || state.PID != 7 {
		t.Errorf("state = %+v, ok = %v", state, ok)
	}
}

func TestStateEventAccountMismatch(t *testing.T) {
	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\nUser=NT SERVICE\\app\n"))

	daemon, network := newTestDaemon(t, scmfake.New(), store)
	err := sendRunnerEvent(t, network, `NT AUTHORITY\SYSTEM`, protocol.StateEvent{
		Unit:  "app.service",
		State: protocol.StateActive,
		PID:   7,
	})
	if err == nil {
		t.Fatal("Send() error = nil, want rejection")
	}
	if _, ok := daemon.State("app.service"); ok {
		t.Error("state event with a mismatched account was applied")
	}
}

func TestCheckRunnersMarksDead(t *testing.T) {
	daemon, _ := newTestDaemon(t, scmfake.New(), storagefake.New())
	daemon.mu.Lock()
	daemon.states["app.service"] = ServiceState{
		Unit:        "app.service",
		RunnerState: RunnerStateActive,
		LastSeen:    time.Now().Add(-2 * config.RunnerDeadAfter),
	}
	daemon.mu.Unlock()

	daemon.checkRunners()

	state, ok := daemon.State("app.service")
	if !ok || state.RunnerState != RunnerStateDead {
		t.Errorf("state = %+v, ok = %v", state, ok)
	}
}

func TestHeartbeatKeepsLastTransition(t *testing.T) {
	daemon, _ := newTestDaemon(t, scmfake.New(), storagefake.New())
	daemon.updateFromEvent(protocol.StateEvent{
		Unit:      "app.service",
		State:     protocol.StateActive,
		PID:       1,
		Timestamp: time.Now().Add(-time.Hour),
	})
	first, _ := daemon.State("app.service")

	daemon.updateFromEvent(protocol.StateEvent{
		Unit:  "app.service",
		State: protocol.StateActive,
		PID:   1,
	})
	second, _ := daemon.State("app.service")

	if !second.LastTransition.Equal(first.LastTransition) {
		t.Errorf("LastTransition moved on heartbeat: %v -> %v", first.LastTransition, second.LastTransition)
	}
	if second.LastSeen.Before(first.LastSeen) {
		t.Errorf("LastSeen moved backwards: %v -> %v", first.LastSeen, second.LastSeen)
	}
}
