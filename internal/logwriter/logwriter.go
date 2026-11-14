package logwriter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const TimestampLayout = "2006-01-02_15-04-05.000"

var managedName = regexp.MustCompile(`^(?:.*_)?(\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}\.\d{3})\.log$`)

type Rotation struct {
	MaxSizeMB  int
	MaxAgeDays int
}

type Writer struct {
	mu       sync.Mutex
	dir      string
	prefix   string
	rotation Rotation
	managed  bool

	file      *os.File
	path      string
	createdAt time.Time
	size      int64
	now       func() time.Time
}

func ManagedPath(dir, prefix string, createdAt time.Time) string {
	return filepath.Join(dir, prefix+createdAt.UTC().Format(TimestampLayout)+".log")
}

func OpenManaged(dir, prefix string, createdAt time.Time, rotation Rotation) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log directory %s: %w", dir, err)
	}

	writer := &Writer{
		dir:      dir,
		prefix:   prefix,
		rotation: rotation,
		managed:  true,
		now:      time.Now,
	}
	if createdAt.IsZero() {
		createdAt = writer.now()
	}
	if err := writer.openManaged(createdAt); err != nil {
		return nil, err
	}
	return writer, nil
}

func OpenFile(path string, truncate bool) (*Writer, error) {
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory for %s: %w", path, err)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if truncate {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}

	file, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log %s: %w", path, err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("stat log %s: %w", path, err)
	}

	return &Writer{
		file:    file,
		path:    path,
		size:    info.Size(),
		managed: false,
		now:     time.Now,
	}, nil
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, os.ErrClosed
	}

	if w.managed {
		now := w.now().UTC()
		rotate := false

		if w.rotation.MaxAgeDays > 0 && !w.createdAt.IsZero() {
			age := time.Duration(w.rotation.MaxAgeDays) * 24 * time.Hour
			if now.Sub(w.createdAt) >= age {
				rotate = true
			}
		}
		if w.rotation.MaxSizeMB > 0 {
			limit := int64(w.rotation.MaxSizeMB) * 1024 * 1024
			if w.size > 0 && w.size+int64(len(p)) > limit {
				rotate = true
			}
		}
		if rotate {
			_ = w.rotate(now)
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *Writer) Path() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.path
}

func (w *Writer) rotate(now time.Time) error {
	oldFile := w.file
	oldPath := w.path
	oldCreated := w.createdAt
	oldSize := w.size

	if err := w.openManaged(now); err != nil {
		w.file = oldFile
		w.path = oldPath
		w.createdAt = oldCreated
		w.size = oldSize
		return err
	}

	return oldFile.Close()
}

func (w *Writer) openManaged(createdAt time.Time) error {
	for attempt := 0; attempt < 1000; attempt++ {
		timestamp := createdAt.UTC().Add(time.Duration(attempt) * time.Millisecond)
		path := filepath.Join(w.dir, w.prefix+timestamp.Format(TimestampLayout)+".log")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("open managed log %s: %w", path, err)
		}
		w.file = file
		w.path = path
		w.createdAt = timestamp
		w.size = 0
		return nil
	}
	return fmt.Errorf("could not allocate a log file name in %s", w.dir)
}

func Cleanup(dir string, maxAgeDays int, now time.Time) error {
	if maxAgeDays <= 0 {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read log directory %s: %w", dir, err)
	}

	cutoff := now.UTC().Add(-time.Duration(maxAgeDays) * 24 * time.Hour)
	var result error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := managedName.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		createdAt, err := time.ParseInLocation(TimestampLayout, match[1], time.UTC)
		if err != nil || !createdAt.Before(cutoff) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, fmt.Errorf("remove old log %s: %w", path, err))
		}
	}
	return result
}

func LatestFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read log directory %s: %w", dir, err)
	}

	var latest string
	var latestTime time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := managedName.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		createdAt, err := time.ParseInLocation(TimestampLayout, match[1], time.UTC)
		if err != nil {
			continue
		}
		if latest == "" || createdAt.After(latestTime) {
			latest = filepath.Join(dir, entry.Name())
			latestTime = createdAt
		}
	}
	return latest, nil
}

func ReadTail(path string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read log %s: %w", path, err)
	}

	text := stringsTrimRightNewlines(string(data))
	if text == "" {
		return nil, nil
	}
	lines := splitLines(text)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

func stringsTrimRightNewlines(value string) string {
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r') {
		value = value[:len(value)-1]
	}
	return value
}

func splitLines(value string) []string {
	lines := make([]string, 0, 16)
	start := 0
	for i := 0; i < len(value); i++ {
		if value[i] != '\n' {
			continue
		}
		line := value[start:i]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		lines = append(lines, line)
		start = i + 1
	}
	line := value[start:]
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	lines = append(lines, line)
	return lines
}
