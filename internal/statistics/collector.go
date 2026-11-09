// Package statistics aggregates proxy traffic in memory and persists minute
// buckets to SQLite. Reads aggregate on demand so the schema stays small.
package statistics

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/proxygate/proxygate/internal/proxy"
	"github.com/proxygate/proxygate/internal/storage"
)

const recentCap = 4096

type bucket struct {
	requests       int64
	in             int64
	out            int64
	s2, s3, s4, s5 int64
	sum            int64
	max            int64
	latencyReqs    int64
	latency        storage.LatencyHistogram
}

// Collector records live traffic and flushes it periodically.
type Collector struct {
	store *storage.Store
	log   *slog.Logger

	mu      sync.Mutex
	buckets map[uint]map[int64]*bucket

	recent   []int64 // duration milliseconds ring
	recentAt int
	recentN  int

	lifetimeMu sync.Mutex
	lifetime   []storage.SiteTraffic
	lifetimeAt time.Time

	pctMu sync.Mutex
	pctAt time.Time
	pct95 int64
	pct99 int64

	done chan struct{}
	once sync.Once
}

// New creates a collector.
func New(store *storage.Store, log *slog.Logger) *Collector {
	if log == nil {
		log = slog.Default()
	}
	return &Collector{store: store, log: log, buckets: make(map[uint]map[int64]*bucket), recent: make([]int64, recentCap), done: make(chan struct{})}
}

// Record implements proxy.Observer's request sink.
func (c *Collector) Record(ev proxy.RequestEvent) {
	minute := ev.Time.UTC().Truncate(time.Minute).Unix()
	ms := ev.Duration.Milliseconds()
	c.mu.Lock()
	if ev.SiteID != 0 {
		byMinute := c.buckets[ev.SiteID]
		if byMinute == nil {
			byMinute = make(map[int64]*bucket)
			c.buckets[ev.SiteID] = byMinute
		}
		b := byMinute[minute]
		if b == nil {
			b = &bucket{}
			byMinute[minute] = b
		}
		b.requests++
		b.in += ev.RequestBytes
		b.out += ev.ResponseBytes
		if !ev.Upgraded {
			b.sum += ms
			b.latencyReqs++
			b.latency.Add(ms)
			if ms > b.max {
				b.max = ms
			}
		}
		switch {
		case ev.Status >= 500:
			b.s5++
		case ev.Status >= 400:
			b.s4++
		case ev.Status >= 300:
			b.s3++
		default:
			b.s2++
		}
	}
	if !ev.Upgraded {
		c.recent[c.recentAt] = ms
		c.recentAt = (c.recentAt + 1) % recentCap
		if c.recentN < recentCap {
			c.recentN++
		}
	}
	c.mu.Unlock()
}

// Start flushes buckets every 10 seconds until ctx is cancelled.
func (c *Collector) Start(ctx context.Context) {
	go func() {
		timer := time.NewTicker(10 * time.Second)
		defer timer.Stop()
		defer close(c.done)
		for {
			select {
			case <-ctx.Done():
				fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = c.Flush(fctx)
				cancel()
				return
			case <-timer.C:
				fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if err := c.Flush(fctx); err != nil {
					c.log.Error("flush traffic statistics", "error", err)
				}
				cancel()
			}
		}
	}()
}

// Wait blocks until the background flusher exits or ctx is done.
func (c *Collector) Wait(ctx context.Context) {
	select {
	case <-c.done:
	case <-ctx.Done():
	}
}

// Flush writes and clears pending buckets.
func (c *Collector) Flush(ctx context.Context) error {
	c.mu.Lock()
	pending := c.buckets
	c.buckets = make(map[uint]map[int64]*bucket)
	c.mu.Unlock()
	deltas := make([]storage.TrafficDelta, 0)
	for siteID, byMinute := range pending {
		for minute, b := range byMinute {
			deltas = append(deltas, storage.TrafficDelta{
				SiteID: siteID, Bucket: time.Unix(minute, 0).UTC(),
				RequestCount: b.requests, InboundBytes: b.in, OutboundBytes: b.out,
				Status2xx: b.s2, Status3xx: b.s3, Status4xx: b.s4, Status5xx: b.s5,
				ResponseSum: b.sum, ResponseMax: b.max,
				LatencyRequests: b.latencyReqs,
				Latency:         b.latency,
			})
		}
	}
	if err := c.store.UpsertTrafficBatch(ctx, deltas); err != nil {
		// The batch is transactional, so merge every pending bucket back before
		// returning; otherwise a single DB error would drop all other buckets.
		c.mergePending(pending)
		return err
	}
	return nil
}

func (c *Collector) mergePending(pending map[uint]map[int64]*bucket) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for siteID, byMinute := range pending {
		for minute, src := range byMinute {
			dstByMinute := c.buckets[siteID]
			if dstByMinute == nil {
				dstByMinute = make(map[int64]*bucket)
				c.buckets[siteID] = dstByMinute
			}
			dst := dstByMinute[minute]
			if dst == nil {
				dstByMinute[minute] = src
				continue
			}
			dst.requests += src.requests
			dst.in += src.in
			dst.out += src.out
			dst.s2 += src.s2
			dst.s3 += src.s3
			dst.s4 += src.s4
			dst.s5 += src.s5
			dst.sum += src.sum
			dst.latencyReqs += src.latencyReqs
			dst.latency.AddHistogram(src.latency)
			if src.max > dst.max {
				dst.max = src.max
			}
		}
	}
}

// Percentiles returns p95 and p99 from the recent request ring.
func (c *Collector) Percentiles() (int64, int64) {
	now := time.Now()
	c.pctMu.Lock()
	if !c.pctAt.IsZero() && now.Sub(c.pctAt) < time.Second {
		p95, p99 := c.pct95, c.pct99
		c.pctMu.Unlock()
		return p95, p99
	}
	c.pctMu.Unlock()

	c.mu.Lock()
	n := c.recentN
	vals := make([]int64, 0, n)
	if n < recentCap {
		vals = append(vals, c.recent[:n]...)
	} else {
		vals = append(vals, c.recent...)
	}
	c.mu.Unlock()
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	p := func(q float64) int64 {
		if len(vals) == 0 {
			return 0
		}
		idx := int(float64(len(vals)-1) * q)
		return vals[idx]
	}
	p95, p99 := p(0.95), p(0.99)
	c.pctMu.Lock()
	c.pctAt = now
	c.pct95, c.pct99 = p95, p99
	c.pctMu.Unlock()
	return p95, p99
}

// Purge removes minute buckets older than the retention window.
func (c *Collector) Purge(ctx context.Context, before time.Time) {
	if _, err := c.store.DeleteTrafficBefore(ctx, before); err != nil {
		c.log.Warn("purge traffic statistics", "error", err)
	}
}
