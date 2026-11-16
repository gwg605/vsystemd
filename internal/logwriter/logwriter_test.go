package logwriter

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gwg605/vsystemd/internal/unit"
)

func TestManagedSizeRotation(t *testing.T) {
	dir := t.TempDir()
	createdAt := time.Date(2026, 11, 11, 20, 0, 0, 0, time.UTC)
	writer, err := OpenManaged(dir, "daemon_", createdAt, Rotation{MaxSizeMB: 1})
	if err != nil {
		t.Fatalf("OpenManaged() error = %v", err)
	}
	writer.now = func() time.Time { return createdAt.Add(time.Hour) }

	chunk := bytes.Repeat([]byte("x"), 700*1024)
	if _, err := writer.Write(chunk); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := writer.Write(chunk); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	secondPath := writer.Path()
	writer.Close()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("files = %d, want 2", len(entries))
	}
	latest, err := LatestFile(dir)
	if err != nil {
		t.Fatalf("LatestFile() error = %v", err)
	}
	if latest != secondPath {
		t.Errorf("LatestFile() = %q, want %q", latest, secondPath)
	}
}

func TestManagedAgeRotation(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 11, 20, 0, 0, 0, time.UTC)
	writer, err := OpenManaged(dir, "daemon_", now.Add(-8*24*time.Hour), Rotation{MaxAgeDays: 7})
	if err != nil {
		t.Fatalf("OpenManaged() error = %v", err)
	}
	writer.now = func() time.Time { return now }

	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	current := writer.Path()
	writer.Close()

	if want := ManagedPath(dir, "daemon_", now); current != want {
		t.Errorf("current = %q, want %q", current, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("files = %d, want 2", len(entries))
	}
}

func TestExplicitTruncateAndAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")

	writer, err := OpenFile(path, true)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	writer.Write([]byte("one"))
	writer.Close()

	writer, err = OpenFile(path, true)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	writer.Write([]byte("two"))
	writer.Close()

	writer, err = OpenFile(path, false)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	writer.Write([]byte("three"))
	writer.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "twothree" {
		t.Errorf("content = %q", data)
	}
}

func TestOpenFileMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "app.log")
	if _, err := OpenFile(path, true); err == nil {
		t.Fatal("OpenFile() error = nil, want error for a missing directory")
	}
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 11, 20, 0, 0, 0, time.UTC)

	oldPath := filepath.Join(dir, "daemon_"+now.Add(-8*24*time.Hour).Format(TimestampLayout)+".log")
	recentPath := filepath.Join(dir, "daemon_"+now.Format(TimestampLayout)+".log")
	unmanagedPath := filepath.Join(dir, "notes.txt")
	for _, path := range []string{oldPath, recentPath, unmanagedPath} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	if err := Cleanup(dir, 7, now); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("old log still exists")
	}
	if _, err := os.Stat(recentPath); err != nil {
		t.Errorf("recent log removed: %v", err)
	}
	if _, err := os.Stat(unmanagedPath); err != nil {
		t.Errorf("unmanaged file removed: %v", err)
	}
}

func TestManagedFileIsReadableWhileOpen(t *testing.T) {
	dir := t.TempDir()
	writer, err := OpenManaged(dir, "daemon_", time.Now().UTC(), Rotation{})
	if err != nil {
		t.Fatalf("OpenManaged() error = %v", err)
	}
	defer writer.Close()

	if _, err := writer.Write([]byte("line\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	data, err := os.ReadFile(writer.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "line\n" {
		t.Errorf("content = %q", data)
	}
}

func TestReadTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, []byte("one\r\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	lines, err := ReadTail(path, 2)
	if err != nil {
		t.Fatalf("ReadTail() error = %v", err)
	}
	if len(lines) != 2 || lines[0] != "two" || lines[1] != "three" {
		t.Errorf("lines = %v", lines)
	}
}

func TestReadTailAcrossChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.log")
	var content bytes.Buffer
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&content, "line-%05d-%s\n", i, strings.Repeat("x", 32))
	}
	if err := os.WriteFile(path, content.Bytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	lines, err := ReadTail(path, 3)
	if err != nil {
		t.Fatalf("ReadTail() error = %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	want := []string{"line-19997", "line-19998", "line-19999"}
	for i, prefix := range want {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Errorf("lines[%d] = %q, want prefix %q", i, lines[i], prefix)
		}
	}
}

func TestResolveServiceOutputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")

	service := &unit.ServiceUnit{
		StandardOutput: "file:" + path,
		StandardError:  "append:" + path,
	}
	stdout, stderr, err := ResolveServiceOutputs(service)
	if err != nil {
		t.Fatalf("ResolveServiceOutputs() error = %v", err)
	}
	if stdout.Kind != OutputFile || stderr.Kind != OutputAppend {
		t.Errorf("kinds = %v/%v", stdout.Kind, stderr.Kind)
	}
	if !SamePath(stdout.Path, stderr.Path) {
		t.Errorf("paths differ: %q/%q", stdout.Path, stderr.Path)
	}

	service = &unit.ServiceUnit{StandardOutput: "", StandardError: "null"}
	stdout, stderr, err = ResolveServiceOutputs(service)
	if err != nil {
		t.Fatalf("ResolveServiceOutputs() error = %v", err)
	}
	if stdout.Kind != OutputDefault || stderr.Kind != OutputNull {
		t.Errorf("kinds = %v/%v", stdout.Kind, stderr.Kind)
	}
}

func TestResolveServiceOutputsErrors(t *testing.T) {
	dotdot := t.TempDir() + string(filepath.Separator) + ".." + string(filepath.Separator) + "app.log"
	tests := map[string]string{
		"unsupported": "journal",
		"relative":    "file:relative.log",
		"parent":      "file:" + dotdot,
		"empty path":  "file:",
	}
	for name, value := range tests {
		service := &unit.ServiceUnit{StandardOutput: value}
		if _, _, err := ResolveServiceOutputs(service); err == nil {
			t.Errorf("%s: ResolveServiceOutputs(%q) error = nil, want error", name, value)
		}
	}
}
