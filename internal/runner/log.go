package runner

import (
	"io"
	"log/slog"
	"time"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/unit"
)

type LogStreamer struct {
	stdout  io.Writer
	stderr  io.Writer
	closers []io.Closer
}

func NewLogStreamer(serviceName string, service *unit.ServiceUnit, logsDir string, rotation logwriter.Rotation, startTime time.Time) (*LogStreamer, error) {
	stdoutTarget, stderrTarget, warnings := logwriter.ResolveServiceOutputs(service)
	for _, warning := range warnings {
		slog.Warn("log: "+warning, "unit", serviceName)
	}

	defaultPath := logwriter.ManagedPath(logwriter.ServiceLogDir(logsDir, serviceName), "", startTime)
	stdout := toWriterSpec(defaultPath, stdoutTarget)
	stderr := toWriterSpec(defaultPath, stderrTarget)

	if stdout.path != "" && stderr.path != "" && logwriter.SamePath(stdout.path, stderr.path) {
		if stdout.kind == logwriter.OutputAppend || stderr.kind == logwriter.OutputAppend {
			stdout.kind = logwriter.OutputAppend
			stderr.kind = logwriter.OutputAppend
		} else if stdout.kind != logwriter.OutputDefault {
			stderr.kind = stdout.kind
		} else if stderr.kind != logwriter.OutputDefault {
			stdout.kind = stderr.kind
		}
	}

	streamer := &LogStreamer{}
	opened := make(map[string]io.Writer)

	open := func(target writerSpec) (io.Writer, error) {
		if target.kind == logwriter.OutputNull {
			return io.Discard, nil
		}
		key := logwriter.CanonicalPath(target.path)
		if existing, ok := opened[key]; ok {
			return existing, nil
		}

		var writer io.Writer
		if target.kind == logwriter.OutputDefault {
			managed, openErr := logwriter.OpenManaged(logwriter.ServiceLogDir(logsDir, serviceName), "", startTime, rotation)
			if openErr != nil {
				return nil, openErr
			}
			streamer.closers = append(streamer.closers, managed)
			writer = managed
		} else {
			explicit, openErr := logwriter.OpenFile(target.path, target.kind == logwriter.OutputFile)
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
	kind logwriter.OutputKind
	path string
}

func toWriterSpec(defaultPath string, target logwriter.OutputTarget) writerSpec {
	if target.Kind == logwriter.OutputDefault {
		return writerSpec{kind: target.Kind, path: defaultPath}
	}
	return writerSpec{kind: target.Kind, path: target.Path}
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
