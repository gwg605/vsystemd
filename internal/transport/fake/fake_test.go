package fake

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/gwg605/vsystemd/internal/transport"
)

func runnerPeer(name string) transport.Peer {
	return transport.NewPeer(transport.PeerRunner, name, false)
}

func TestSendAndRequest(t *testing.T) {
	network := NewNetwork()
	events := make(chan transport.Message, 1)

	var server *Transport
	server = network.New(transport.DefaultConfig(), runnerPeer("server"), func(source transport.Peer, msg transport.Message) error {
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
	defer server.Close()

	client := network.New(transport.DefaultConfig(), runnerPeer("client"), nil)
	defer client.Close()

	if err := client.Send(runnerPeer("server"), transport.Message{Type: "state_event"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if msg := <-events; msg.Type != "state_event" {
		t.Errorf("event = %+v", msg)
	}

	resp, err := client.Req(runnerPeer("server"), transport.Message{
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

func TestMissingPeer(t *testing.T) {
	network := NewNetwork()
	client := network.New(transport.DefaultConfig(), runnerPeer("client"), nil)
	defer client.Close()

	if err := client.Send(runnerPeer("missing"), transport.Message{Type: "state_event"}); err == nil {
		t.Error("Send() error = nil for missing peer")
	}
}

func TestClose(t *testing.T) {
	network := NewNetwork()
	client := network.New(transport.DefaultConfig(), runnerPeer("client"), nil)
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := client.Send(runnerPeer("peer"), transport.Message{Type: "state_event"}); !errors.Is(err, transport.ErrClosed) {
		t.Errorf("Send() error = %v", err)
	}
	if _, err := client.Req(runnerPeer("peer"), transport.Message{Type: "status_request"}); !errors.Is(err, transport.ErrClosed) {
		t.Errorf("Req() error = %v", err)
	}
}
