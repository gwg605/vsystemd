package runner

import (
	"encoding/json"
	"testing"

	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/transport/fake"
)

type stateCollector struct {
	events chan StateEvent
}

func newStateCollector() *stateCollector {
	return &stateCollector{events: make(chan StateEvent, 16)}
}

func (c *stateCollector) handler(_ transport.Peer, msg transport.Message) error {
	if msg.Type != MessageStateEvent {
		return nil
	}
	var event StateEvent
	if err := json.Unmarshal(msg.Payload, &event); err != nil {
		return err
	}
	c.events <- event
	return nil
}

func newReporterPair(t *testing.T, unitName string) (*StateReporter, *fake.Transport, *stateCollector) {
	t.Helper()
	network := fake.NewNetwork()

	collector := newStateCollector()
	daemon := network.New(transport.DefaultConfig(), transport.NewPeer(transport.PeerDaemon, daemonPeer, false), collector.handler)
	t.Cleanup(func() { daemon.Close() })

	var reporter *StateReporter
	runnerTransport := network.New(transport.DefaultConfig(), transport.NewPeer(transport.PeerRunner, unitName, false), func(source transport.Peer, msg transport.Message) error {
		return reporter.Handle(source, msg)
	})
	t.Cleanup(func() { runnerTransport.Close() })
	reporter = NewStateReporter(runnerTransport, unitName)

	return reporter, daemon, collector
}

func TestStateReporterReport(t *testing.T) {
	reporter, _, collector := newReporterPair(t, "app.service")

	if err := reporter.Report(StateEvent{State: StateActive, PID: 123, RestartCount: 2}); err != nil {
		t.Fatalf("Report() error = %v", err)
	}

	event := <-collector.events
	if event.Unit != "app.service" {
		t.Errorf("Unit = %q", event.Unit)
	}
	if event.State != StateActive || event.PID != 123 || event.RestartCount != 2 {
		t.Errorf("event = %+v", event)
	}
	if event.Timestamp.IsZero() {
		t.Error("Timestamp is zero")
	}
}

func TestStateReporterStatusRequest(t *testing.T) {
	reporter, daemon, _ := newReporterPair(t, "app.service")

	want := StateEvent{State: StateActive, PID: 456, ExitCode: 1, RestartCount: 3, LastError: "boom"}
	if err := reporter.Report(want); err != nil {
		t.Fatalf("Report() error = %v", err)
	}

	resp, err := daemon.Req(daemon.Target(transport.PeerRunner, transport.WithName("app.service")), transport.Message{Type: MessageStatusRequest})
	if err != nil {
		t.Fatalf("Req() error = %v", err)
	}
	if resp.Type != MessageStateEvent || resp.RequestID == "" {
		t.Errorf("response = %+v", resp)
	}

	var got StateEvent
	if err := json.Unmarshal(resp.Payload, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Unit != "app.service" || got.State != want.State || got.PID != want.PID ||
		got.ExitCode != want.ExitCode || got.RestartCount != want.RestartCount || got.LastError != want.LastError {
		t.Errorf("state = %+v", got)
	}
}
