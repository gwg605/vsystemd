//go:build windows

package pipe

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/transport"
)

func TestEnvelopeScannerSizeLimit(t *testing.T) {
	scanner := newEnvelopeScanner(bytes.NewReader(bytes.Repeat([]byte("a"), 128)), 64)
	if scanner.Scan() {
		t.Error("Scan() = true for an oversized line")
	}
	if !errors.Is(scanner.Err(), bufio.ErrTooLong) {
		t.Errorf("Err() = %v, want bufio.ErrTooLong", scanner.Err())
	}
}

func TestEnvelopeScannerReadsLine(t *testing.T) {
	line := `{"from":"x","msg":{"id":"1"}}`
	scanner := newEnvelopeScanner(bytes.NewReader([]byte(line+"\n")), 1024)
	if !scanner.Scan() {
		t.Fatalf("Scan() = false: %v", scanner.Err())
	}
	if string(scanner.Bytes()) != line {
		t.Errorf("line = %q", scanner.Bytes())
	}
}

func TestSendAndRequest(t *testing.T) {
	old := daemonPipeName
	daemonPipeName = `\\.\pipe\vsystemd-test-pipe`
	t.Cleanup(func() { daemonPipeName = old })

	const clientPeer = "test-client.service"

	events := make(chan transport.Message, 1)
	var server transport.Transport
	server, err := newTestTransport(transport.DefaultConfig(), transport.NewPeer(transport.PeerDaemon, "daemon", false), func(source transport.Peer, msg transport.Message) error {
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
	}, fixedAuth{transport.NewPeer(transport.PeerRunner, clientPeer, false)})
	if err != nil {
		t.Fatalf("server New() error = %v", err)
	}
	defer server.Close()

	client, err := newTestTransport(transport.DefaultConfig(), transport.NewPeer(transport.PeerRunner, clientPeer, false), nil, nil)
	if err != nil {
		t.Fatalf("client New() error = %v", err)
	}
	defer client.Close()

	target := client.Target(transport.PeerDaemon)

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

func TestSendToMissingDaemon(t *testing.T) {
	old := daemonPipeName
	daemonPipeName = `\\.\pipe\vsystemd-test-missing`
	t.Cleanup(func() { daemonPipeName = old })

	client, err := newTestTransport(transport.DefaultConfig(), transport.NewPeer(transport.PeerRunner, "test-missing-client.service", false), nil, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer client.Close()

	err = client.Send(client.Target(transport.PeerDaemon),
		transport.Message{Type: "state_event"},
		transport.WithConnectTimeout(50*time.Millisecond))
	if err == nil {
		t.Fatal("Send() error = nil")
	}
}
