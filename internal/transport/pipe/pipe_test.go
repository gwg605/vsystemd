//go:build windows

package pipe

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/transport"
)

func TestPipeName(t *testing.T) {
	tests := map[string]string{
		"daemon":         `\\.\pipe\vsystemd-in`,
		"foo.service":    `\\.\pipe\vsystemd-svc-foo.service`,
		"systemctl-4321": `\\.\pipe\vsystemd-ctl-4321`,
	}
	for peer, want := range tests {
		if got := pipeName(peer); got != want {
			t.Errorf("pipeName(%q) = %q, want %q", peer, got, want)
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
	server, err = New(transport.DefaultConfig(), serverPeer, func(source string, msg transport.Message) error {
		if source != clientPeer {
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

	client, err := New(transport.DefaultConfig(), clientPeer, nil)
	if err != nil {
		t.Fatalf("client New() error = %v", err)
	}
	defer client.Close()

	if err := client.Send(serverPeer, transport.Message{
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

	resp, err := client.Req(serverPeer, transport.Message{
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
	client, err := New(transport.DefaultConfig(), "test-missing-client.service", nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer client.Close()

	err = client.Send("test-missing-target.service", transport.Message{Type: "state_event"},
		transport.WithConnectTimeout(50*time.Millisecond))
	if err == nil {
		t.Fatal("Send() error = nil")
	}
}
