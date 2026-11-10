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

func (l *link) sender(from string) SendFunc {
	return func(target string, msg Message, _ ...Opt) error {
		l.mu.Lock()
		core := l.cores[target]
		l.mu.Unlock()
		if core == nil {
			return fmt.Errorf("unknown peer %s", target)
		}
		return core.Dispatch(from, msg)
	}
}

func newEchoPair(t *testing.T) (*Core, *Core) {
	t.Helper()
	l := newLink()
	var responder *Core
	responder = NewCore(DefaultConfig(), func(source string, msg Message) error {
		return responder.Send(source, Message{
			Type:      "pong",
			RequestID: msg.ID,
			Payload:   msg.Payload,
		})
	}, l.sender("b"))
	requester := NewCore(DefaultConfig(), nil, l.sender("a"))
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

	resp, err := requester.Request("b", Message{
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
			resp, err := requester.Request("b", Message{
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
	responder := NewCore(DefaultConfig(), func(string, Message) error {
		return nil
	}, l.sender("b"))
	requester := NewCore(DefaultConfig(), nil, l.sender("a"))
	l.add("a", requester)
	l.add("b", responder)
	t.Cleanup(func() {
		requester.Close()
		responder.Close()
	})

	_, err := requester.Request("b", Message{Type: "ping"}, WithTimeout(50*time.Millisecond))
	if err == nil {
		t.Fatal("Request() error = nil")
	}
}

func TestSendAssignsID(t *testing.T) {
	var sent Message
	core := NewCore(DefaultConfig(), nil, func(_ string, msg Message, _ ...Opt) error {
		sent = msg
		return nil
	})
	t.Cleanup(core.Close)

	if err := core.Send("peer", Message{Type: "state_event"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	first := sent.ID
	if first == "" {
		t.Fatal("Send() did not assign an id")
	}

	if err := core.Send("peer", Message{Type: "state_event"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if sent.ID == first {
		t.Error("Send() reused an id")
	}
}

func TestClose(t *testing.T) {
	core := NewCore(DefaultConfig(), nil, func(string, Message, ...Opt) error {
		return nil
	})
	core.Close()

	if err := core.Send("peer", Message{Type: "state_event"}); !errors.Is(err, ErrClosed) {
		t.Errorf("Send() error = %v", err)
	}
	if _, err := core.Request("peer", Message{Type: "status_request"}); !errors.Is(err, ErrClosed) {
		t.Errorf("Request() error = %v", err)
	}
}
