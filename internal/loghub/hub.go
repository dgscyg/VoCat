package loghub

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is the stable, centrally-redacted representation exposed by the log
// API. The Hub sanitizes both the downstream handler and the captured entry so
// diagnostic logs can be safely exported by users.
type Entry struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Caller  string         `json:"caller,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
}

type core struct {
	mu          sync.RWMutex
	capacity    int
	entries     []Entry
	subscribers map[uint64]chan Entry
	nextID      uint64
}

// Hub is both a slog.Handler and a bounded live log source.
type Hub struct {
	next   slog.Handler
	core   *core
	attrs  []slog.Attr
	groups []string
}

func New(next slog.Handler, capacity int) *Hub {
	if next == nil {
		next = slog.NewTextHandler(discardWriter{}, nil)
	}
	if capacity < 100 {
		capacity = 100
	}
	return &Hub{
		next: next,
		core: &core{
			capacity:    capacity,
			entries:     make([]Entry, 0, capacity),
			subscribers: make(map[uint64]chan Entry),
		},
	}
}

func (h *Hub) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *Hub) Handle(ctx context.Context, record slog.Record) error {
	record = sanitizeRecord(record)
	err := h.next.Handle(ctx, record)
	fields := make(map[string]any)
	for _, attr := range h.attrs {
		appendAttribute(fields, h.groups, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		appendAttribute(fields, h.groups, attr)
		return true
	})
	entry := Entry{
		Time:    record.Time.UTC(),
		Level:   levelName(record.Level),
		Message: record.Message,
		Fields:  fields,
	}
	if len(fields) == 0 {
		entry.Fields = nil
	}
	h.publish(entry)
	return err
}

func (h *Hub) WithAttrs(attrs []slog.Attr) slog.Handler {
	attrs = sanitizeAttrs(attrs)
	nextAttrs := append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &Hub{
		next:   h.next.WithAttrs(attrs),
		core:   h.core,
		attrs:  nextAttrs,
		groups: append([]string(nil), h.groups...),
	}
}

func (h *Hub) WithGroup(name string) slog.Handler {
	name = strings.TrimSpace(name)
	groups := append([]string(nil), h.groups...)
	if name != "" {
		groups = append(groups, name)
	}
	return &Hub{
		next:   h.next.WithGroup(name),
		core:   h.core,
		attrs:  append([]slog.Attr(nil), h.attrs...),
		groups: groups,
	}
}

func (h *Hub) publish(entry Entry) {
	h.core.mu.Lock()
	if len(h.core.entries) == h.core.capacity {
		copy(h.core.entries, h.core.entries[1:])
		h.core.entries[len(h.core.entries)-1] = cloneEntry(entry)
	} else {
		h.core.entries = append(h.core.entries, cloneEntry(entry))
	}
	for _, subscriber := range h.core.subscribers {
		select {
		case subscriber <- cloneEntry(entry):
		default:
			select {
			case <-subscriber:
			default:
			}
			select {
			case subscriber <- cloneEntry(entry):
			default:
			}
		}
	}
	h.core.mu.Unlock()
}

// History returns the newest matching entries in chronological order.
func (h *Hub) History(limit int, minimum slog.Level, search string) []Entry {
	if limit < 1 {
		limit = 1
	}
	if limit > h.core.capacity {
		limit = h.core.capacity
	}
	search = strings.ToLower(strings.TrimSpace(search))
	h.core.mu.RLock()
	result := make([]Entry, 0, limit)
	for index := len(h.core.entries) - 1; index >= 0 && len(result) < limit; index-- {
		entry := h.core.entries[index]
		if parseLevel(entry.Level) < minimum {
			continue
		}
		if search != "" && !entryContains(entry, search) {
			continue
		}
		result = append(result, cloneEntry(entry))
	}
	h.core.mu.RUnlock()
	sort.SliceStable(result, func(i, j int) bool { return result[i].Time.Before(result[j].Time) })
	return result
}

func (h *Hub) Subscribe(buffer int) (<-chan Entry, func()) {
	if buffer < 1 {
		buffer = 1
	}
	if buffer > 1000 {
		buffer = 1000
	}
	channel := make(chan Entry, buffer)
	h.core.mu.Lock()
	id := h.core.nextID
	h.core.nextID++
	h.core.subscribers[id] = channel
	h.core.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.core.mu.Lock()
			delete(h.core.subscribers, id)
			close(channel)
			h.core.mu.Unlock()
		})
	}
	return channel, cancel
}

// Clear drops captured history and every entry currently queued for live and
// persistence subscribers. Subscribers stay connected for future events.
func (h *Hub) Clear() {
	h.core.mu.Lock()
	h.core.entries = h.core.entries[:0]
	for _, subscriber := range h.core.subscribers {
		for {
			select {
			case <-subscriber:
				continue
			default:
			}
			break
		}
	}
	h.core.mu.Unlock()
}

func appendAttribute(fields map[string]any, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	target := fields
	for _, group := range groups {
		next, ok := target[group].(map[string]any)
		if !ok {
			next = make(map[string]any)
			target[group] = next
		}
		target = next
	}
	if attr.Value.Kind() == slog.KindGroup {
		group := make(map[string]any)
		for _, child := range attr.Value.Group() {
			appendAttribute(group, nil, child)
		}
		target[attr.Key] = group
		return
	}
	target[attr.Key] = attr.Value.Any()
}

func levelName(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "error"
	case level >= slog.LevelWarn:
		return "warn"
	case level >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}

func parseLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "error":
		return slog.LevelError
	case "warn", "warning":
		return slog.LevelWarn
	case "info", "":
		return slog.LevelInfo
	default:
		return slog.LevelDebug
	}
}

func entryContains(entry Entry, search string) bool {
	if strings.Contains(strings.ToLower(entry.Message), search) ||
		strings.Contains(strings.ToLower(entry.Caller), search) {
		return true
	}
	for key, value := range entry.Fields {
		if strings.Contains(strings.ToLower(key), search) ||
			strings.Contains(strings.ToLower(toString(value)), search) {
			return true
		}
	}
	return false
}

func cloneEntry(entry Entry) Entry {
	if entry.Fields != nil {
		entry.Fields = cloneMap(entry.Fields)
	}
	return entry
}

func cloneMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		if nested, ok := value.(map[string]any); ok {
			result[key] = cloneMap(nested)
		} else {
			result[key] = value
		}
	}
	return result
}

func toString(value any) string {
	if stringValue, ok := value.(string); ok {
		return stringValue
	}
	return slog.AnyValue(value).String()
}

type discardWriter struct{}

func (discardWriter) Write(data []byte) (int, error) {
	return len(data), nil
}
