package runner

import (
	"io"
	"path/filepath"
	"time"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/unit"
)

type logStreamer struct {
	stdout  io.Writer
	stderr  io.Writer
	closers []io.Closer
}

func newLogStreamer(store storage.Storage, serviceName string, service *unit.ServiceUnit, logsDir string, rotation logwriter.Rotation, startTime time.Time) (*logStreamer, error) {
	stdoutTarget, stderrTarget, err := service.ResolveOutputs()
	if err != nil {
		return nil, err
	}

	defaultPath := logwriter.ManagedPath(logwriter.ServiceLogDir(logsDir, serviceName), "", startTime)
	stdout, err := toWriterSpec(store, defaultPath, stdoutTarget)
	if err != nil {
		return nil, err
	}
	stderr, err := toWriterSpec(store, defaultPath, stderrTarget)
	if err != nil {
		return nil, err
	}

	if stdout.path != "" && stderr.path != "" && logwriter.SamePath(stdout.path, stderr.path) {
		if stdout.kind == unit.OutputAppend || stderr.kind == unit.OutputAppend {
			stdout.kind = unit.OutputAppend
			stderr.kind = unit.OutputAppend
		} else if stdout.kind != unit.OutputDefault {
			stderr.kind = stdout.kind
		} else if stderr.kind != unit.OutputDefault {
			stdout.kind = stderr.kind
		}
	}

	streamer := &logStreamer{}
	opened := make(map[string]io.Writer)

	open := func(target writerSpec) (io.Writer, error) {
		if target.kind == unit.OutputNull {
			return io.Discard, nil
		}
		key := logwriter.CanonicalPath(target.path)
		if existing, ok := opened[key]; ok {
			return existing, nil
		}

		var writer io.Writer
		if target.kind == unit.OutputDefault {
			managed, openErr := logwriter.OpenManaged(logwriter.ServiceLogDir(logsDir, serviceName), "", startTime, rotation)
			if openErr != nil {
				return nil, openErr
			}
			streamer.closers = append(streamer.closers, managed)
			writer = managed
		} else {
			explicit, openErr := logwriter.OpenFile(target.path, target.kind == unit.OutputFile)
			if openErr != nil {
				return nil, openErr
			}
			streamer.closers = append(streamer.closers, explicit)
			writer = explicit
		}

		opened[key] = writer
		return writer, nil
	}

	stdoutWriter, err := open(stdout)
	if err != nil {
		streamer.Close()
		return nil, err
	}
	streamer.stdout = stdoutWriter

	stderrWriter, err := open(stderr)
	if err != nil {
		streamer.Close()
		return nil, err
	}
	streamer.stderr = stderrWriter

	return streamer, nil
}

type writerSpec struct {
	kind unit.OutputKind
	path string
}

func toWriterSpec(store storage.Storage, defaultPath string, target unit.OutputTarget) (writerSpec, error) {
	switch target.Kind {
	case unit.OutputDefault:
		return writerSpec{kind: target.Kind, path: defaultPath}, nil
	case unit.OutputNull:
		return writerSpec{kind: target.Kind}, nil
	default:
		if err := store.ValidatePath(target.Path); err != nil {
			return writerSpec{}, err
		}
		return writerSpec{kind: target.Kind, path: filepath.Clean(target.Path)}, nil
	}
}

func (l *logStreamer) Stdout() io.Writer {
	return l.stdout
}

func (l *logStreamer) Stderr() io.Writer {
	return l.stderr
}

func (l *logStreamer) Close() error {
	var firstErr error
	for _, closer := range l.closers {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	l.closers = nil
	return firstErr
}
