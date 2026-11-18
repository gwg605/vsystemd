package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func recvOpResult(t *testing.T, results <-chan opResult) opResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not report completion")
		return opResult{}
	}
}

func startProcessOperation(t *testing.T, line string, report func(opResult)) *processOperation {
	t.Helper()
	parsed, err := parseCommand(line)
	if err != nil {
		t.Fatalf("parseCommand() error = %v", err)
	}
	cmd := exec.Command(parsed.path, parsed.args...)
	configureCommand(cmd)
	op := newProcessOperation(roleMain, cmd, report)
	if err := op.start(); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	return op
}

func TestProcessOperationSuccess(t *testing.T) {
	file := filepath.Join(t.TempDir(), "out.txt")
	results := make(chan opResult, 1)

	startProcessOperation(t, helperCommand("record", file, "done"), func(r opResult) { results <- r })

	result := recvOpResult(t, results)
	if result.err != nil || result.code != 0 || result.cancelled {
		t.Errorf("result = %+v", result)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "done\n" {
		t.Errorf("output = %q", data)
	}
}

func TestProcessOperationExitCode(t *testing.T) {
	file := filepath.Join(t.TempDir(), "out.txt")
	results := make(chan opResult, 1)

	startProcessOperation(t, helperCommand("record", file, "done", "3"), func(r opResult) { results <- r })

	result := recvOpResult(t, results)
	if result.err != nil || result.code != 3 || result.cancelled {
		t.Errorf("result = %+v", result)
	}
}

func TestProcessOperationCancel(t *testing.T) {
	results := make(chan opResult, 1)
	op := startProcessOperation(t, helperCommand("sleep", "5s"), func(r opResult) { results <- r })

	op.cancel(cancelKill)

	result := recvOpResult(t, results)
	if !result.cancelled {
		t.Errorf("result = %+v", result)
	}
}

func newTestSequence(t *testing.T, results chan opResult, lines ...string) *sequenceOperation {
	t.Helper()
	commands, err := parseCommands(lines)
	if err != nil {
		t.Fatalf("parseCommands() error = %v", err)
	}
	op := newSequenceOperation(rolePre, commands, nil, "", nil, nil, func(r opResult) { results <- r })
	if err := op.start(); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	return op
}

func TestSequenceOperationSuccess(t *testing.T) {
	file := filepath.Join(t.TempDir(), "order.txt")
	results := make(chan opResult, 1)

	newTestSequence(t, results,
		helperCommand("record", file, "one"),
		helperCommand("record", file, "two"))

	result := recvOpResult(t, results)
	if result.err != nil || result.code != 0 || result.cancelled {
		t.Errorf("result = %+v", result)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read order: %v", err)
	}
	if string(data) != "one\ntwo\n" {
		t.Errorf("order = %q", data)
	}
}

func TestSequenceOperationFailureStops(t *testing.T) {
	file := filepath.Join(t.TempDir(), "order.txt")
	results := make(chan opResult, 1)

	newTestSequence(t, results,
		helperCommand("record", file, "one", "1"),
		helperCommand("record", file, "two"))

	result := recvOpResult(t, results)
	if result.err != nil || result.code != 1 || result.cancelled {
		t.Errorf("result = %+v", result)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read order: %v", err)
	}
	if string(data) != "one\n" {
		t.Errorf("order = %q", data)
	}
}

func TestSequenceOperationIgnoreFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "order.txt")
	results := make(chan opResult, 1)

	newTestSequence(t, results,
		"-"+helperCommand("record", file, "one", "1"),
		helperCommand("record", file, "two"))

	result := recvOpResult(t, results)
	if result.err != nil || result.code != 0 || result.cancelled {
		t.Errorf("result = %+v", result)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read order: %v", err)
	}
	if string(data) != "one\ntwo\n" {
		t.Errorf("order = %q", data)
	}
}

func TestSequenceOperationEmpty(t *testing.T) {
	results := make(chan opResult, 1)

	newTestSequence(t, results)

	result := recvOpResult(t, results)
	if result.err != nil || result.code != 0 || result.cancelled {
		t.Errorf("result = %+v", result)
	}
}
