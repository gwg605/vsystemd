//go:build windows

package pipe

import (
	"testing"

	"github.com/gwg605/vsystemd/internal/transport"
)

func TestDaemonControlEcho(t *testing.T) {
	old := daemonPipeName
	daemonPipeName = `\\.\pipe\vsystemd-test-in`
	t.Cleanup(func() { daemonPipeName = old })

	var daemon transport.Transport
	daemon, err := New(transport.DefaultConfig(), transport.NewPeer(transport.PeerDaemon, "daemon", false), func(source transport.Peer, msg transport.Message) error {
		return daemon.Send(source, transport.Message{Type: "pong", RequestID: msg.ID, Payload: msg.Payload})
	})
	if err != nil {
		t.Fatalf("daemon New: %v", err)
	}
	defer daemon.Close()

	control, err := New(transport.DefaultConfig(), transport.NewPeer(transport.PeerControl, "systemctl_1", false), nil)
	if err != nil {
		t.Fatalf("control New: %v", err)
	}
	defer control.Close()

	resp, err := control.Req(control.Target(transport.PeerDaemon), transport.Message{Type: "ping"})
	if err != nil {
		t.Fatalf("Req: %v", err)
	}
	if resp.Type != "pong" {
		t.Fatalf("resp = %+v", resp)
	}
}
