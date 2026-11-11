package logging

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	mu   sync.Mutex
	file *os.File
)

func Configure(path, level string) error {
	if path == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}

	opened, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", path, err)
	}

	handler := &handler{
		mu:     &sync.Mutex{},
		writer: opened,
		level:  parseLevel(level),
	}

	mu.Lock()
	previous := file
	file = opened
	mu.Unlock()

	slog.SetDefault(slog.New(handler))
	if previous != nil {
		previous.Close()
	}
	return nil
}

func Close() error {
	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return nil
	}
	err := file.Close()
	file = nil
	return err
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type handler struct {
	mu     *sync.Mutex
	writer *os.File
	level  slog.Level
	attrs  []slog.Attr
	groups []string
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *handler) Handle(_ context.Context, record slog.Record) error {
	timestamp := record.Time
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	var buffer bytes.Buffer
	buffer.WriteString(timestamp.Format("2006-01-02 15:04:05.000"))
	buffer.WriteString(" [")
	buffer.WriteString(record.Level.String())
	buffer.WriteString("] ")
	buffer.WriteString(record.Message)

	for _, attr := range h.attrs {
		appendAttr(&buffer, h.groups, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		appendAttr(&buffer, h.groups, attr)
		return true
	})
	buffer.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.writer.Write(buffer.Bytes())
	return err
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &clone
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.groups = append(append([]string{}, h.groups...), name)
	return &clone
}

func appendAttr(buffer *bytes.Buffer, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}

	if attr.Value.Kind() == slog.KindGroup {
		group := groups
		if attr.Key != "" {
			group = append(append([]string{}, groups...), attr.Key)
		}
		for _, child := range attr.Value.Group() {
			appendAttr(buffer, group, child)
		}
		return
	}

	key := attr.Key
	if len(groups) > 0 {
		key = strings.Join(groups, ".") + "." + key
	}
	buffer.WriteByte(' ')
	buffer.WriteString(key)
	buffer.WriteByte('=')
	buffer.WriteString(formatValue(attr.Value))
}

func formatValue(value slog.Value) string {
	switch value.Kind() {
	case slog.KindString:
		text := value.String()
		if text == "" || strings.ContainsAny(text, " \t\r\n\"=") {
			return strconv.Quote(text)
		}
		return text
	case slog.KindTime:
		return value.Time().Format(time.RFC3339)
	default:
		return value.String()
	}
}
