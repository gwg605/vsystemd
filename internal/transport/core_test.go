package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type link struct {
	mu    sync.Mutex
	cores map[string]*Core
}

func newLink() *link {
	return &link{cores: make(map[string]*Core)}
}

func (l *link) add(name string, core *Core) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cores[name] = core
}

func (l *link) sender(local Peer) SendFunc {
	return func(target Peer, msg Message, _ ...Opt) error {
		l.mu.Lock()
		core := l.cores[target.Name()]
		l.mu.Unlock()
		if core == nil {
			return fmt.Errorf("unknown peer %s", target.Name())
		}
		return core.Dispatch(local, msg)
	}
}

func newEchoPair(t *testing.T) (*Core, *Core) {
	t.Helper()
	l := newLink()
	var responder *Core
	responder = NewCore(DefaultConfig(), func(source Peer, msg Message) error {
		return responder.Send(source, Message{
			Type:      "pong",
			RequestID: msg.ID,
			Payload:   msg.Payload,
		})
	}, l.sender(NewPeer(PeerRunner, "b", false)))
	requester := NewCore(DefaultConfig(), nil, l.sender(NewPeer(PeerRunner, "a", false)))
	l.add("a", requester)
	l.add("b", responder)
	t.Cleanup(func() {
		requester.Close()
		responder.Close()
	})
	return requester, responder
}

func TestRequest(t *testing.T) {
	requester, _ := newEchoPair(t)

	resp, err := requester.Request(NewPeer(PeerRunner, "b", false), Message{
		Type:    "ping",
		Payload: json.RawMessage(`{"n":1}`),
	})
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	if resp.Type != "pong" {
		t.Errorf("Type = %q", resp.Type)
	}
	if resp.RequestID == "" || resp.ID == resp.RequestID {
		t.Errorf("ids = %q/%q", resp.ID, resp.RequestID)
	}
	if string(resp.Payload) != `{"n":1}` {
		t.Errorf("Payload = %s", resp.Payload)
	}
}

func TestConcurrentRequests(t *testing.T) {
	requester, _ := newEchoPair(t)

	const count = 10
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			payload := fmt.Sprintf(`{"n":%d}`, n)
			resp, err := requester.Request(NewPeer(PeerRunner, "b", false), Message{
				Type:    "ping",
				Payload: json.RawMessage(payload),
			})
			if err != nil {
				errs <- err
				return
			}
			if string(resp.Payload) != payload {
				errs <- fmt.Errorf("payload = %s, want %s", resp.Payload, payload)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestRequestTimeout(t *testing.T) {
	l := newLink()
	responder := NewCore(DefaultConfig(), func(Peer, Message) error {
		return nil
	}, l.sender(NewPeer(PeerRunner, "b", false)))
	requester := NewCore(DefaultConfig(), nil, l.sender(NewPeer(PeerRunner, "a", false)))
	l.add("a", requester)
	l.add("b", responder)
	t.Cleanup(func() {
		requester.Close()
		responder.Close()
	})

	_, err := requester.Request(NewPeer(PeerRunner, "b", false), Message{Type: "ping"}, WithTimeout(50*time.Millisecond))
	if err == nil {
		t.Fatal("Request() error = nil")
	}
}

func TestResponseFromWrongPeerDropped(t *testing.T) {
	l := newLink()
	requester := NewCore(DefaultConfig(), nil, l.sender(NewPeer(PeerRunner, "a", false)))
	l.add("a", requester)
	t.Cleanup(requester.Close)

	// A response whose source does not match the request target must be dropped.
	requester.mu.Lock()
	response := make(chan Message, 1)
	requester.pending["req-1"] = &pendingRequest{expected: NewPeer(PeerRunner, "b", false), response: response}
	requester.mu.Unlock()

	if err := requester.Dispatch(NewPeer(PeerRunner, "c", false), Message{RequestID: "req-1", Type: "pong"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	select {
	case <-response:
		t.Error("response from wrong peer was delivered")
	default:
	}
}

func TestSendAssignsID(t *testing.T) {
	var sent Message
	core := NewCore(DefaultConfig(), nil, func(_ Peer, msg Message, _ ...Opt) error {
		sent = msg
		return nil
	})
	t.Cleanup(core.Close)

	if err := core.Send(NewPeer(PeerRunner, "peer", false), Message{Type: "state_event"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	first := sent.ID
	if first == "" {
		t.Fatal("Send() did not assign an id")
	}

	if err := core.Send(NewPeer(PeerRunner, "peer", false), Message{Type: "state_event"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if sent.ID == first {
		t.Error("Send() reused an id")
	}
}

func TestTargetDefaultsDaemonName(t *testing.T) {
	core := NewCore(DefaultConfig(), nil, func(Peer, Message, ...Opt) error { return nil })
	t.Cleanup(core.Close)

	daemon := core.Target(PeerDaemon)
	if daemon.Type() != PeerDaemon || daemon.Name() != PeerDaemonName || !daemon.Valid() {
		t.Errorf("Target(PeerDaemon) = %+v", daemon)
	}

	runner := core.Target(PeerRunner, WithName("app.service"))
	if runner.Type() != PeerRunner || runner.Name() != "app.service" || !runner.Valid() {
		t.Errorf("Target(PeerRunner) = %+v", runner)
	}
}

func TestClose(t *testing.T) {
	core := NewCore(DefaultConfig(), nil, func(Peer, Message, ...Opt) error {
		return nil
	})
	core.Close()

	if err := core.Send(NewPeer(PeerRunner, "peer", false), Message{Type: "state_event"}); !errors.Is(err, ErrClosed) {
		t.Errorf("Send() error = %v", err)
	}
	if _, err := core.Request(NewPeer(PeerRunner, "peer", false), Message{Type: "status_request"}); !errors.Is(err, ErrClosed) {
		t.Errorf("Request() error = %v", err)
	}
}
