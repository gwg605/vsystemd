//go:build windows

package pipe

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/transport"
)

func TestPipeName(t *testing.T) {
	tests := []struct {
		peer transport.Peer
		want string
	}{
		{transport.NewPeer(transport.PeerDaemon, "daemon", false), `\\.\pipe\vsystemd-in`},
		{transport.NewPeer(transport.PeerRunner, "foo.service", false), `\\.\pipe\vsystemd-svc-foo.service`},
		{transport.NewPeer(transport.PeerControl, "systemctl_4321", false), `\\.\pipe\vsystemd-ctl-4321`},
		{transport.NewPeer(transport.PeerControl, "systemctl-4321", false), `\\.\pipe\vsystemd-ctl-4321`},
	}
	for _, test := range tests {
		if got := pipeName(test.peer); got != test.want {
			t.Errorf("pipeName(%v) = %q, want %q", test.peer, got, test.want)
		}
	}
}

func TestSendAndRequest(t *testing.T) {
	const (
		clientPeer = "test-client.service"
		serverPeer = "test-server.service"
	)

	events := make(chan transport.Message, 1)
	var server transport.Transport
	var err error
	server, err = New(transport.DefaultConfig(), transport.NewPeer(transport.PeerRunner, serverPeer, false), func(source transport.Peer, msg transport.Message) error {
		if source.Name() != clientPeer {
			return nil
		}
		switch msg.Type {
		case "state_event":
			events <- msg
		case "ping":
			return server.Send(source, transport.Message{
				Type:      "pong",
				RequestID: msg.ID,
				Payload:   msg.Payload,
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("server New() error = %v", err)
	}
	defer server.Close()

	client, err := New(transport.DefaultConfig(), transport.NewPeer(transport.PeerRunner, clientPeer, false), nil)
	if err != nil {
		t.Fatalf("client New() error = %v", err)
	}
	defer client.Close()

	target := transport.NewPeer(transport.PeerRunner, serverPeer, false)

	if err := client.Send(target, transport.Message{
		Type:    "state_event",
		Payload: json.RawMessage(`{"state":"active"}`),
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	select {
	case msg := <-events:
		if msg.Type != "state_event" || string(msg.Payload) != `{"state":"active"}` {
			t.Errorf("event = %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("state event was not delivered")
	}

	resp, err := client.Req(target, transport.Message{
		Type:    "ping",
		Payload: json.RawMessage(`{"n":1}`),
	})
	if err != nil {
		t.Fatalf("Req() error = %v", err)
	}
	if resp.Type != "pong" || string(resp.Payload) != `{"n":1}` {
		t.Errorf("response = %+v", resp)
	}
}

func TestSendToMissingPeer(t *testing.T) {
	client, err := New(transport.DefaultConfig(), transport.NewPeer(transport.PeerRunner, "test-missing-client.service", false), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer client.Close()

	err = client.Send(transport.NewPeer(transport.PeerRunner, "test-missing-target.service", false),
		transport.Message{Type: "state_event"},
		transport.WithConnectTimeout(50*time.Millisecond))
	if err == nil {
		t.Fatal("Send() error = nil")
	}
}
