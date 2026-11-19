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

func newReporterPair(t *testing.T, unitName string) (*stateReporter, *fake.Transport, *stateCollector) {
	t.Helper()
	network := fake.NewNetwork()

	collector := newStateCollector()
	daemon := network.New(transport.DefaultConfig(), transport.NewPeer(transport.PeerDaemon, daemonPeer, false), collector.handler)
	t.Cleanup(func() { daemon.Close() })

	var reporter *stateReporter
	runnerTransport := network.New(transport.DefaultConfig(), transport.NewPeer(transport.PeerRunner, unitName, false), nil)
	t.Cleanup(func() { runnerTransport.Close() })
	reporter = newStateReporter(runnerTransport, unitName)

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
