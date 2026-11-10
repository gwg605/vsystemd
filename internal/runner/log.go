package runner

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/unit"
)

type LogStreamer struct {
	stdout  io.Writer
	stderr  io.Writer
	closers []io.Closer
}

func NewLogStreamer(name string, service *unit.ServiceUnit, store storage.Storage) (*LogStreamer, error) {
	streamer := &LogStreamer{}

	stdout, stdoutCloser, err := logWriter(name, "stdout", service.StandardOutput, store)
	if err != nil {
		return nil, fmt.Errorf("StandardOutput: %w", err)
	}
	streamer.stdout = stdout
	if stdoutCloser != nil {
		streamer.closers = append(streamer.closers, stdoutCloser)
	}

	stderr, stderrCloser, err := logWriter(name, "stderr", service.StandardError, store)
	if err != nil {
		streamer.Close()
		return nil, fmt.Errorf("StandardError: %w", err)
	}
	streamer.stderr = stderr
	if stderrCloser != nil {
		streamer.closers = append(streamer.closers, stderrCloser)
	}

	return streamer, nil
}

func (l *LogStreamer) Stdout() io.Writer {
	return l.stdout
}

func (l *LogStreamer) Stderr() io.Writer {
	return l.stderr
}

func (l *LogStreamer) Close() error {
	var firstErr error
	for _, closer := range l.closers {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	l.closers = nil
	return firstErr
}

func logWriter(name, stream, value string, store storage.Storage) (io.Writer, io.Closer, error) {
	if strings.HasPrefix(value, "file:") {
		path := strings.TrimPrefix(value, "file:")
		if path == "" {
			return nil, nil, fmt.Errorf("empty file path")
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file %s: %w", path, err)
		}
		return file, file, nil
	}
	return &storageWriter{store: store, name: name, stream: stream}, nil, nil
}

type storageWriter struct {
	store  storage.Storage
	name   string
	stream string
}

func (w *storageWriter) Write(data []byte) (int, error) {
	if err := w.store.WriteLog(w.name, w.stream, data); err != nil {
		return 0, err
	}
	return len(data), nil
}
