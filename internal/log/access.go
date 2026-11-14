// Package applog provides the access log pipeline (ring buffer + JSONL files)
// and the event hub used by Server-Sent Events.
package applog

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Entry is one access log record.
type Entry struct {
	Time          time.Time `json:"time"`
	SiteID        uint      `json:"site_id"`
	SiteName      string    `json:"site_name"`
	Domain        string    `json:"domain"`
	ClientIP      string    `json:"client_ip"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	Protocol      string    `json:"protocol"`
	Status        int       `json:"status"`
	DurationMS    int64     `json:"duration_ms"`
	Upgraded      bool      `json:"upgraded,omitempty"`
	Application   string    `json:"application"`
	RequestBytes  int64     `json:"request_bytes"`
	ResponseBytes int64     `json:"response_bytes"`
	UserAgent     string    `json:"user_agent"`
	RequestID     string    `json:"request_id"`
}

// Filter selects access log entries from the in-memory ring.
type Filter struct {
	SiteID    uint
	ClientIP  string
	Status    int
	Method    string
	Path      string
	RequestID string
	From      *time.Time
	To        *time.Time
	Page      int
	PageSize  int
}

// Hub broadcasts events to SSE subscribers. Slow subscribers lose events
// instead of blocking the proxy.
type Hub struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

// Event is one SSE payload.
type Event struct {
	Type string    `json:"type"`
	Time time.Time `json:"time"`
	Data any       `json:"data"`
}

// NewHub creates an empty hub.
func NewHub() *Hub { return &Hub{subs: make(map[chan Event]struct{})} }

// Publish sends e to every subscriber without blocking.
func (h *Hub) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Subscribe returns a channel and a cancel function.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// AccessLog writes entries to a ring buffer, the hub and daily JSONL files.
type AccessLog struct {
	dataDir   string
	hub       *Hub
	log       *slog.Logger
	retain    time.Duration
	maxBytes  int64
	mu        sync.RWMutex
	ring      []Entry
	cap       int
	next      int
	full      bool
	ch        chan Entry
	dropped   atomic.Int64
	closeOnce sync.Once
	done      chan struct{}
	sendMu    sync.RWMutex
	closed    bool
}

// NewAccessLog creates the asynchronous writer. Call Close on shutdown.
func NewAccessLog(dataDir string, hub *Hub, retainDays int, maxMB int64, log *slog.Logger) *AccessLog {
	if retainDays <= 0 {
		retainDays = 14
	}
	if maxMB <= 0 {
		maxMB = 512
	}
	a := &AccessLog{
		dataDir: dataDir, hub: hub, log: log, retain: time.Duration(retainDays) * 24 * time.Hour,
		maxBytes: maxMB << 20, cap: 5000, ch: make(chan Entry, 8192), done: make(chan struct{}),
	}
	a.ring = make([]Entry, a.cap)
	a.seedRecent(2)
	go a.writer()
	return a
}

// seedRecent loads the tail of the last few daily files into the in-memory
// ring. The ring is memory-only, so without this the logs page would look empty
// after a restart even though the JSONL files are intact.
func (a *AccessLog) seedRecent(days int) {
	now := time.Now()
	buf := make([]Entry, a.cap)
	count, pos := 0, 0
	add := func(e Entry) {
		buf[pos] = e
		pos = (pos + 1) % a.cap
		if count < a.cap {
			count++
		}
	}
	for d := 0; d < days; d++ {
		path, err := a.FileForDate(now.AddDate(0, 0, -d).Format("2006-01-02"))
		if err != nil {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			var e Entry
			if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
				continue
			}
			e.Path = RedactRequestURI(e.Path)
			add(e)
		}
		_ = file.Close()
	}
	if count == 0 {
		return
	}
	start := (pos - count + a.cap) % a.cap
	for i := 0; i < count; i++ {
		a.ring[a.next] = buf[(start+i)%a.cap]
		a.next = (a.next + 1) % a.cap
		if a.next == 0 {
			a.full = true
		}
	}
}

// Record enqueues an entry. It never blocks the request path.
func (a *AccessLog) Record(e Entry) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Path = RedactRequestURI(e.Path)
	a.mu.Lock()
	a.ring[a.next] = e
	a.next = (a.next + 1) % a.cap
	if a.next == 0 {
		a.full = true
	}
	a.mu.Unlock()
	a.sendMu.RLock()
	defer a.sendMu.RUnlock()
	if a.closed {
		return
	}
	select {
	case a.ch <- e:
	default:
		a.dropped.Add(1)
	}
	if a.hub != nil {
		a.hub.Publish(Event{Type: "access", Time: e.Time, Data: e})
	}
}

// Query returns matching entries newest first.
// Since returns recent entries newer than from, newest first. It is used for
// low-cost live counters such as topology QPS.
func (a *AccessLog) Since(from time.Time) []Entry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	n := a.cap
	if !a.full {
		n = a.next
	}
	out := make([]Entry, 0, 64)
	for i := 0; i < n; i++ {
		idx := (a.next - 1 - i + a.cap*2) % a.cap
		e := a.ring[idx]
		if e.Time.IsZero() || e.Time.Before(from) {
			break
		}
		out = append(out, e)
	}
	return out
}

func (a *AccessLog) Query(f Filter) ([]Entry, int64) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 500 {
		f.PageSize = 50
	}
	a.mu.RLock()
	var all []Entry
	n := a.cap
	if !a.full {
		n = a.next
	}
	for i := 0; i < n; i++ {
		idx := (a.next - 1 - i + a.cap*2) % a.cap
		e := a.ring[idx]
		if e.Time.IsZero() {
			break
		}
		e.Path = RedactRequestURI(e.Path)
		if !matchesFilter(e, f) {
			continue
		}
		all = append(all, e)
	}
	a.mu.RUnlock()
	total := int64(len(all))
	start := (f.Page - 1) * f.PageSize
	if start >= len(all) {
		return []Entry{}, total
	}
	end := start + f.PageSize
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], total
}

// Dropped returns the number of entries dropped due to backpressure.
func (a *AccessLog) Dropped() int64 { return a.dropped.Load() }

func matchesFilter(e Entry, f Filter) bool {
	if f.SiteID != 0 && e.SiteID != f.SiteID {
		return false
	}
	if f.ClientIP != "" && !strings.Contains(e.ClientIP, f.ClientIP) {
		return false
	}
	if f.Status != 0 && e.Status != f.Status {
		return false
	}
	if f.Method != "" && !strings.EqualFold(e.Method, f.Method) {
		return false
	}
	if f.Path != "" && !strings.Contains(e.Path, f.Path) {
		return false
	}
	if f.RequestID != "" && e.RequestID != f.RequestID {
		return false
	}
	if f.From != nil && e.Time.Before(*f.From) {
		return false
	}
	if f.To != nil && !e.Time.Before(*f.To) {
		return false
	}
	return true
}

func (a *AccessLog) writer() {
	defer close(a.done)
	var (
		f       *os.File
		bw      *bufio.Writer
		day     string
		lastGC  time.Time
		flushAt = time.Now()
	)
	open := func(day string) error {
		if f != nil {
			_ = bw.Flush()
			_ = f.Close()
		}
		dir := filepath.Join(a.dataDir, "logs", "access")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
		file, err := os.OpenFile(filepath.Join(dir, day+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
		if err != nil {
			return err
		}
		f, bw = file, bufio.NewWriterSize(file, 64*1024)
		return nil
	}
	write := func(e Entry) {
		d := e.Time.Format("2006-01-02")
		if d != day {
			if err := open(d); err != nil {
				a.log.Error("open access log", "error", err)
				return
			}
			day = d
		}
		line, err := json.Marshal(e)
		if err != nil {
			return
		}
		if _, err := bw.Write(line); err != nil {
			return
		}
		if err := bw.WriteByte('\n'); err != nil {
			return
		}
		if bw.Buffered() > 512*1024 {
			_ = bw.Flush()
		}
	}
	for {
		select {
		case e, ok := <-a.ch:
			if !ok {
				if bw != nil {
					_ = bw.Flush()
				}
				if f != nil {
					_ = f.Close()
				}
				return
			}
			write(e)
			if time.Since(flushAt) > time.Second {
				_ = bw.Flush()
				flushAt = time.Now()
			}
			if f != nil {
				if info, err := f.Stat(); err == nil && info.Size() > 256<<20 {
					_ = bw.Flush()
					_ = f.Close()
					rotated := filepath.Join(a.dataDir, "logs", "access", day+"-"+time.Now().Format("150405")+".jsonl")
					_ = os.Rename(filepath.Join(a.dataDir, "logs", "access", day+".jsonl"), rotated)
					f = nil
					if err := open(day); err != nil {
						a.log.Error("rotate access log", "error", err)
					}
				}
			}
			if time.Since(lastGC) > 6*time.Hour {
				a.gc()
				lastGC = time.Now()
			}
		}
	}
}

// Close drains the queue and stops the writer.
func (a *AccessLog) Close(ctx context.Context) error {
	a.closeOnce.Do(func() {
		a.sendMu.Lock()
		a.closed = true
		close(a.ch)
		a.sendMu.Unlock()
	})
	select {
	case <-a.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *AccessLog) gc() {
	dir := filepath.Join(a.dataDir, "logs", "access")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type fileInfo struct {
		name string
		size int64
		mod  time.Time
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{e.Name(), info.Size(), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	var total int64
	cutoff := time.Now().Add(-a.retain)
	for _, f := range files {
		total += f.size
		if f.mod.Before(cutoff) || total > a.maxBytes {
			if err := os.Remove(filepath.Join(dir, f.name)); err != nil {
				a.log.Warn("remove old access log", "file", f.name, "error", err)
			}
		}
	}
}

// FileForDate returns the log file path for YYYY-MM-DD if it exists.
func (a *AccessLog) FileForDate(date string) (string, error) {
	if len(date) != 10 {
		return "", fmt.Errorf("invalid date")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", fmt.Errorf("invalid date")
	}
	p := filepath.Join(a.dataDir, "logs", "access", date+".jsonl")
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}
