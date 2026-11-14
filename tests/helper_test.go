//go:build windows

package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/daemon"
	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/runner"
	scmfake "github.com/gwg605/vsystemd/internal/scm/fake"
	storagefake "github.com/gwg605/vsystemd/internal/storage/fake"
	"github.com/gwg605/vsystemd/internal/transport"
	transportfake "github.com/gwg605/vsystemd/internal/transport/fake"
	"github.com/gwg605/vsystemd/internal/unit"
)

var clientCounter atomic.Int64

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	args := os.Args
	index := 0
	for index < len(args) && args[index] != "--" {
		index++
	}
	if index+1 >= len(args) {
		os.Exit(2)
	}
	args = args[index+1:]

	switch args[0] {
	case "sleep":
		duration, err := time.ParseDuration(args[1])
		if err != nil {
			os.Exit(2)
		}
		time.Sleep(duration)
		os.Exit(0)
	case "record":
		code := 0
		if len(args) > 3 {
			code, _ = strconv.Atoi(args[3])
		}
		file, err := os.OpenFile(args[1], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			os.Exit(2)
		}
		fmt.Fprintln(file, args[2])
		file.Close()
		os.Exit(code)
	case "failonce":
		if _, err := os.Stat(args[1]); err == nil {
			os.Exit(0)
		}
		if err := os.WriteFile(args[1], []byte("failed"), 0o644); err != nil {
			os.Exit(2)
		}
		os.Exit(1)
	}
	os.Exit(2)
}

func helperCommand(args ...string) string {
	parts := []string{quoteArg(os.Args[0]), "-test.run=TestHelperProcess", "--"}
	for _, arg := range args {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteArg(arg string) string {
	if arg == "" || strings.ContainsAny(arg, " \t") {
		return `"` + arg + `"`
	}
	return arg
}

type testEnv struct {
	manager *scmfake.Manager
	store   *storagefake.Storage
	network *transportfake.Network
}

func newTestEnv() *testEnv {
	return &testEnv{
		manager: scmfake.New(),
		store:   storagefake.New(),
		network: transportfake.NewNetwork(),
	}
}

func (e *testEnv) transportFactory(peer string, handler transport.MsgHandler) (transport.Transport, error) {
	return e.network.New(transport.DefaultConfig(), peer, handler), nil
}

func (e *testEnv) newDaemon(t *testing.T) *daemon.Daemon {
	t.Helper()
	d, err := daemon.New(e.manager, e.store, `C:\vsystemd\vsystemd.exe`, t.TempDir(), logwriter.Rotation{}, e.transportFactory)
	if err != nil {
		t.Fatalf("daemon.New() error = %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func (e *testEnv) newRunner(t *testing.T, name string, service *unit.ServiceUnit) *runner.Runner {
	t.Helper()
	serviceName := strings.TrimSuffix(name, filepath.Ext(name))
	logs, err := runner.NewLogStreamer(serviceName, service, t.TempDir(), logwriter.Rotation{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("runner.NewLogStreamer() error = %v", err)
	}
	r, err := runner.NewRunner(name, service, logs, e.transportFactory)
	if err != nil {
		logs.Close()
		t.Fatalf("runner.NewRunner() error = %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func (e *testEnv) newClient(t *testing.T) *transportfake.Transport {
	t.Helper()
	peer := fmt.Sprintf("systemctl-%d", clientCounter.Add(1))
	client := e.network.New(transport.DefaultConfig(), peer, nil)
	t.Cleanup(func() { client.Close() })
	return client
}

func daemonRequest(t *testing.T, client transport.Transport, msgType string, payload any) transport.Message {
	t.Helper()
	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s: %v", msgType, err)
		}
		raw = data
	}
	response, err := client.Req(protocol.DaemonPeer, transport.Message{Type: msgType, Payload: raw})
	if err != nil {
		t.Fatalf("%s request: %v", msgType, err)
	}
	return response
}

func startRunner(t *testing.T, r *runner.Runner) (chan svc.ChangeRequest, <-chan svc.Status, <-chan struct{}) {
	t.Helper()
	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 20)
	done := make(chan struct{})
	go func() {
		r.Serve(requests, changes)
		close(done)
	}()
	return requests, changes, done
}

func waitForStatus(t *testing.T, changes <-chan svc.Status, state svc.State) {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case status := <-changes:
			if status.State == state {
				return
			}
		case <-timeout.C:
			t.Fatalf("state %v was not reported", state)
		}
	}
}

func waitForDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not stop")
	}
}

func waitForFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("read %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForState(t *testing.T, d *daemon.Daemon, unitName string, state daemon.RunnerState) daemon.ServiceState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, ok := d.State(unitName)
		if ok && current.RunnerState == state {
			return current
		}
		if time.Now().After(deadline) {
			t.Fatalf("state %q was not observed for %s", state, unitName)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
