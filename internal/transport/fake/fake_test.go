package fake

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/gwg605/vsystemd/internal/transport"
)

func TestSendAndRequest(t *testing.T) {
	network := NewNetwork()
	events := make(chan transport.Message, 1)

	var server *Transport
	server = network.New(transport.DefaultConfig(), "server", func(source string, msg transport.Message) error {
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

	client := network.New(transport.DefaultConfig(), "client", nil)
	defer client.Close()

	if err := client.Send("server", transport.Message{Type: "state_event"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if msg := <-events; msg.Type != "state_event" {
		t.Errorf("event = %+v", msg)
	}

	resp, err := client.Req("server", transport.Message{
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
	client := network.New(transport.DefaultConfig(), "client", nil)
	defer client.Close()

	if err := client.Send("missing", transport.Message{Type: "state_event"}); err == nil {
		t.Error("Send() error = nil for missing peer")
	}
}

func TestClose(t *testing.T) {
	network := NewNetwork()
	client := network.New(transport.DefaultConfig(), "client", nil)
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := client.Send("peer", transport.Message{Type: "state_event"}); !errors.Is(err, transport.ErrClosed) {
		t.Errorf("Send() error = %v", err)
	}
	if _, err := client.Req("peer", transport.Message{Type: "status_request"}); !errors.Is(err, transport.ErrClosed) {
		t.Errorf("Req() error = %v", err)
	}
}
