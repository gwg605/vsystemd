//go:build windows

package pipe

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/gwg605/vsystemd/internal/daemon"
	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/protocol"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unitpath"
)

func TestDaemonRealHandlerOverPipe(t *testing.T) {
	old := daemonPipeName
	daemonPipeName = `\\.\pipe\vsystemd-test-daemon`
	t.Cleanup(func() { daemonPipeName = old })

	store := storagefake.New()
	store.WriteUnit("app.service", []byte("[Service]\nExecStart=x\n"))
	manager := scmfake.New()

	controlName := fmt.Sprintf("systemctl_%d", os.Getpid())
	controlPeer := transport.NewPeer(transport.PeerControl, controlName, true)

	d, err := daemon.New(manager, store, `C:\vsystemd\vsystemd.exe`, unitpath.Roots{LogsDir: t.TempDir()}, logwriter.Rotation{},
		func(local transport.Peer, handler transport.MsgHandler) (transport.Transport, error) {
			return newTestTransport(transport.DefaultConfig(), local, handler, fixedAuth{controlPeer})
		})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	control, err := newTestTransport(transport.DefaultConfig(), transport.NewPeer(transport.PeerControl, controlName, false), nil, nil)
	if err != nil {
		t.Fatalf("control New: %v", err)
	}
	t.Cleanup(func() { control.Close() })

	resp, err := control.Req(control.Target(transport.PeerDaemon), transport.Message{Type: protocol.TypeListUnits})
	if err != nil {
		t.Fatalf("Req: %v", err)
	}
	if resp.Type != protocol.TypeListUnitsResponse {
		var failure protocol.ErrorResponse
		_ = json.Unmarshal(resp.Payload, &failure)
		t.Fatalf("resp type = %q error = %q", resp.Type, failure.Error)
	}
}
