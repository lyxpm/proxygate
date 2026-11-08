package applog

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/proxygate/proxygate/internal/storage"
)

// System writes structured system log rows asynchronously and publishes them
// to the SSE hub.
type System struct {
	store *storage.Store
	hub   *Hub
	log   *slog.Logger
	ch    chan storage.SystemLog
	done  chan struct{}
	once  sync.Once

	mu     sync.RWMutex
	closed bool
}

// NewSystem starts the system log writer.
func NewSystem(store *storage.Store, hub *Hub, log *slog.Logger) *System {
	s := &System{store: store, hub: hub, log: log, ch: make(chan storage.SystemLog, 1024), done: make(chan struct{})}
	go s.writer()
	return s
}

// Logf records a system event.
func (s *System) Logf(ctx context.Context, level, category, message string, fields map[string]any) {
	raw := ""
	if len(fields) > 0 {
		if b, err := json.Marshal(fields); err == nil {
			raw = string(b)
		}
	}
	row := storage.SystemLog{Level: level, Category: category, Message: message, Fields: raw}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return
	}
	select {
	case s.ch <- row:
	default:
		s.log.Warn("system log queue full, dropping entry", "category", category, "message", message)
	}
	s.mu.RUnlock()
	if s.hub != nil {
		s.hub.Publish(Event{Type: "system", Time: time.Now(), Data: map[string]any{
			"level": level, "category": category, "message": message, "fields": fields,
		}})
	}
	_ = ctx
}

func (s *System) writer() {
	defer close(s.done)
	for row := range s.ch {
		if err := s.store.InsertSystemLog(context.Background(), &row); err != nil {
			s.log.Error("insert system log", "error", err)
		}
	}
}

// Close flushes pending rows.
func (s *System) Close(ctx context.Context) {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.ch)
		s.mu.Unlock()
	})
	select {
	case <-s.done:
	case <-ctx.Done():
	}
}
