package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gwg605/vsystemd/internal/protocol"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
	"github.com/gwg605/vsystemd/internal/transport"
)

func TestControlRequiresElevation(t *testing.T) {
	_, network := newTestDaemon(t, scmfake.New(), storagefake.New())

	client := network.New(transport.DefaultConfig(), transport.NewPeer(transport.PeerControl, "systemctl_2", false), nil)
	t.Cleanup(func() { client.Close() })

	response, err := client.Req(client.Target(transport.PeerDaemon), transport.Message{Type: protocol.TypeListUnits})
	if err != nil {
		t.Fatalf("Req() error = %v", err)
	}
	assertForbidden(t, response, "elevation")
}

func TestControlRejectsRunner(t *testing.T) {
	_, network := newTestDaemon(t, scmfake.New(), storagefake.New())

	client := network.New(transport.DefaultConfig(), transport.NewPeer(transport.PeerRunner, "app.service", false), nil)
	t.Cleanup(func() { client.Close() })

	response, err := client.Req(client.Target(transport.PeerDaemon), transport.Message{Type: protocol.TypeListUnits})
	if err != nil {
		t.Fatalf("Req() error = %v", err)
	}
	assertForbidden(t, response, "control peer")
}

func TestStateEventUnitMismatch(t *testing.T) {
	daemon, network := newTestDaemon(t, scmfake.New(), storagefake.New())

	runnerTransport := network.New(transport.DefaultConfig(), transport.NewPeer(transport.PeerRunner, "app.service", false), nil)
	t.Cleanup(func() { runnerTransport.Close() })

	payload, err := json.Marshal(protocol.StateEvent{Unit: "other.service", State: protocol.StateActive, PID: 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	err = runnerTransport.Send(runnerTransport.Target(transport.PeerDaemon), transport.Message{
		Type:    protocol.MessageStateEvent,
		Payload: payload,
	})
	if err == nil {
		t.Fatal("Send() error = nil, want rejection")
	}
	if _, ok := daemon.State("other.service"); ok {
		t.Error("state event for a foreign unit was applied")
	}
}

func TestStateEventFromControlRejected(t *testing.T) {
	daemon, network := newTestDaemon(t, scmfake.New(), storagefake.New())

	client := network.New(transport.DefaultConfig(), transport.NewPeer(transport.PeerControl, "systemctl_3", true), nil)
	t.Cleanup(func() { client.Close() })

	payload, err := json.Marshal(protocol.StateEvent{Unit: "app.service", State: protocol.StateActive})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := client.Send(client.Target(transport.PeerDaemon), transport.Message{
		Type:    protocol.MessageStateEvent,
		Payload: payload,
	}); err == nil {
		t.Fatal("Send() error = nil, want rejection")
	}
	if _, ok := daemon.State("app.service"); ok {
		t.Error("state event from a control peer was applied")
	}
}

func assertForbidden(t *testing.T, response transport.Message, want string) {
	t.Helper()
	if response.Type != protocol.TypeErrorResponse {
		t.Fatalf("response type = %q, want error", response.Type)
	}
	var failure protocol.ErrorResponse
	if err := json.Unmarshal(response.Payload, &failure); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if !strings.Contains(failure.Error, want) {
		t.Errorf("error = %q, want containing %q", failure.Error, want)
	}
}
