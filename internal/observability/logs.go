package observability

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type LogEntry struct {
	Time       time.Time      `json:"time"`
	Level      string         `json:"level"`
	Message    string         `json:"message"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type LogStore struct {
	mu       sync.RWMutex
	capacity int
	entries  []LogEntry
}

func NewLogStore(capacity int) *LogStore {
	if capacity < 1 {
		capacity = 1000
	}
	return &LogStore{capacity: capacity}
}

func (s *LogStore) Enabled(context.Context, slog.Level) bool { return true }

func (s *LogStore) Handle(ctx context.Context, record slog.Record) error {
	return (&storeHandler{store: s}).Handle(ctx, record)
}

func (s *LogStore) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &storeHandler{store: s, attrs: append([]slog.Attr(nil), attrs...)}
}

func (s *LogStore) WithGroup(name string) slog.Handler {
	return &storeHandler{store: s, groups: []string{name}}
}

type storeHandler struct {
	store  *LogStore
	attrs  []slog.Attr
	groups []string
}

func (h *storeHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *storeHandler) Handle(_ context.Context, record slog.Record) error {
	attributes := make(map[string]any)
	for _, attr := range h.attrs {
		addAttr(attributes, h.groups, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		addAttr(attributes, h.groups, attr)
		return true
	})
	entry := LogEntry{
		Time:       record.Time.UTC(),
		Level:      strings.ToLower(record.Level.String()),
		Message:    record.Message,
		Attributes: attributes,
	}
	h.store.mu.Lock()
	if len(h.store.entries) == h.store.capacity {
		copy(h.store.entries, h.store.entries[1:])
		h.store.entries[len(h.store.entries)-1] = entry
	} else {
		h.store.entries = append(h.store.entries, entry)
	}
	h.store.mu.Unlock()
	return nil
}

func (h *storeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &storeHandler{
		store:  h.store,
		attrs:  append(append([]slog.Attr(nil), h.attrs...), attrs...),
		groups: append([]string(nil), h.groups...),
	}
}

func (h *storeHandler) WithGroup(name string) slog.Handler {
	return &storeHandler{
		store:  h.store,
		attrs:  append([]slog.Attr(nil), h.attrs...),
		groups: append(append([]string(nil), h.groups...), name),
	}
}

func (s *LogStore) Recent(limit int, minimum slog.Level) []LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.entries) {
		limit = len(s.entries)
	}
	result := make([]LogEntry, 0, limit)
	for index := len(s.entries) - 1; index >= 0 && len(result) < limit; index-- {
		entry := s.entries[index]
		if parseLevel(entry.Level) < minimum {
			continue
		}
		result = append(result, entry)
	}
	return result
}

type multiHandler struct {
	handlers []slog.Handler
}

func MultiHandler(handlers ...slog.Handler) slog.Handler {
	return multiHandler{handlers: handlers}
}

func (m multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range m.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m multiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range m.handlers {
		if handler.Enabled(ctx, record.Level) {
			if err := handler.Handle(ctx, record.Clone()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, 0, len(m.handlers))
	for _, handler := range m.handlers {
		next = append(next, handler.WithAttrs(attrs))
	}
	return multiHandler{handlers: next}
}

func (m multiHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, 0, len(m.handlers))
	for _, handler := range m.handlers {
		next = append(next, handler.WithGroup(name))
	}
	return multiHandler{handlers: next}
}

func ParseLevel(value string) slog.Level {
	return parseLevel(value)
}

func parseLevel(value string) slog.Level {
	switch strings.ToLower(value) {
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

func addAttr(target map[string]any, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	current := target
	for _, group := range groups {
		child, _ := current[group].(map[string]any)
		if child == nil {
			child = make(map[string]any)
			current[group] = child
		}
		current = child
	}
	if attr.Value.Kind() == slog.KindGroup {
		child := make(map[string]any)
		for _, grouped := range attr.Value.Group() {
			addAttr(child, nil, grouped)
		}
		current[attr.Key] = child
		return
	}
	value := attr.Value.Any()
	if err, ok := value.(error); ok {
		value = err.Error()
	}
	if _, ok := value.(fmt.Stringer); ok {
		value = fmt.Sprint(value)
	}
	current[attr.Key] = value
}
