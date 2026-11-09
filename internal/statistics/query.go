package statistics

import (
	"context"
	"time"

	"github.com/proxygate/proxygate/internal/storage"
)

// Totals is an aggregated range.
type Totals struct {
	Requests  int64 `json:"requests"`
	Inbound   int64 `json:"inbound_bytes"`
	Outbound  int64 `json:"outbound_bytes"`
	Status2xx int64 `json:"status_2xx"`
	Status3xx int64 `json:"status_3xx"`
	Status4xx int64 `json:"status_4xx"`
	Status5xx int64 `json:"status_5xx"`
	AvgMS     int64 `json:"avg_ms"`
	MaxMS     int64 `json:"max_ms"`
	P95MS     int64 `json:"p95_ms"`
	P99MS     int64 `json:"p99_ms"`
}

// Point is one time-series bucket.
type Point struct {
	Time      time.Time `json:"time"`
	Requests  int64     `json:"requests"`
	Inbound   int64     `json:"inbound_bytes"`
	Outbound  int64     `json:"outbound_bytes"`
	Status2xx int64     `json:"status_2xx"`
	Status3xx int64     `json:"status_3xx"`
	Status4xx int64     `json:"status_4xx"`
	Status5xx int64     `json:"status_5xx"`
	AvgMS     int64     `json:"avg_ms"`
	P95MS     int64     `json:"p95_ms"`
	P99MS     int64     `json:"p99_ms"`
	hist      storage.LatencyHistogram
}

// Totals aggregates traffic over [from, to).
func (c *Collector) Totals(ctx context.Context, from, to time.Time) (Totals, error) {
	row, err := c.store.TrafficTotals(ctx, from, to)
	if err != nil {
		return Totals{}, err
	}
	t := Totals{
		Requests: row.RequestCount, Inbound: row.InboundBytes, Outbound: row.OutboundBytes,
		Status2xx: row.Status2xx, Status3xx: row.Status3xx, Status4xx: row.Status4xx, Status5xx: row.Status5xx,
		MaxMS: row.ResponseTimeMax,
	}
	if row.RequestCount > 0 {
		t.AvgMS = row.ResponseTimeSum / row.RequestCount
	}
	hist := storage.DecodeLatencyHistogram(row.LatencyHist)
	t.P95MS = hist.Percentile(row.RequestCount, 0.95)
	t.P99MS = hist.Percentile(row.RequestCount, 0.99)
	return t, nil
}

// Series aggregates minute rows into buckets of step width.
func (c *Collector) Series(ctx context.Context, siteID uint, from, to time.Time, step time.Duration) ([]Point, error) {
	if step < time.Minute {
		step = time.Minute
	}
	rows, err := c.store.QueryTraffic(ctx, siteID, from, to)
	if err != nil {
		return nil, err
	}
	index := make(map[int64]*Point)
	for _, r := range rows {
		key := r.BucketTime.UTC().Truncate(step).Unix()
		p := index[key]
		if p == nil {
			p = &Point{Time: time.Unix(key, 0).UTC()}
			index[key] = p
		}
		p.Requests += r.RequestCount
		p.Inbound += r.InboundBytes
		p.Outbound += r.OutboundBytes
		p.Status2xx += r.Status2xx
		p.Status3xx += r.Status3xx
		p.Status4xx += r.Status4xx
		p.Status5xx += r.Status5xx
		if r.RequestCount > 0 {
			p.AvgMS += r.ResponseTimeSum
		}
		p.hist.AddHistogram(storage.DecodeLatencyHistogram(r.LatencyHist))
	}
	// Keep the chart's time axis continuous even when there are no requests
	// in a bucket. Without this, a sparse day collapses to a single point and
	// a line chart degenerates into a vertical mark.
	start := from.UTC().Truncate(step)
	end := to.UTC().Truncate(step)
	if end.Before(start) {
		end = start
	}
	out := make([]Point, 0, int(end.Sub(start)/step)+1)
	for key := start; !key.After(end); key = key.Add(step) {
		p := index[key.Unix()]
		if p == nil {
			p = &Point{Time: key}
		} else if p.Requests > 0 {
			// ResponseTimeSum was accumulated in AvgMS temporarily.
			p.AvgMS /= p.Requests
			p.P95MS = p.hist.Percentile(p.Requests, 0.95)
			p.P99MS = p.hist.Percentile(p.Requests, 0.99)
		}
		out = append(out, *p)
	}
	return out, nil
}

// SiteTraffic is a per-site aggregate with name filled by the caller.
type SiteTraffic struct {
	SiteID    uint  `json:"site_id"`
	Requests  int64 `json:"requests"`
	Inbound   int64 `json:"inbound_bytes"`
	Outbound  int64 `json:"outbound_bytes"`
	Status2xx int64 `json:"status_2xx"`
	Status3xx int64 `json:"status_3xx"`
	Status4xx int64 `json:"status_4xx"`
	Status5xx int64 `json:"status_5xx"`
	AvgMS     int64 `json:"avg_ms"`
	P95MS     int64 `json:"p95_ms"`
	P99MS     int64 `json:"p99_ms"`
}

// LifetimeBySite returns all-time per-site totals with a short cache. The
// topology view refreshes every few seconds and must not scan the whole traffic
// table on every refresh.
func (c *Collector) LifetimeBySite(ctx context.Context, now time.Time) ([]storage.SiteTraffic, error) {
	c.lifetimeMu.Lock()
	if !c.lifetimeAt.IsZero() && now.Sub(c.lifetimeAt) < 30*time.Second {
		out := append([]storage.SiteTraffic(nil), c.lifetime...)
		c.lifetimeMu.Unlock()
		return out, nil
	}
	c.lifetimeMu.Unlock()
	rows, err := c.store.TrafficBySite(ctx, time.Unix(0, 0), now)
	if err != nil {
		return nil, err
	}
	c.lifetimeMu.Lock()
	c.lifetime = append([]storage.SiteTraffic(nil), rows...)
	c.lifetimeAt = now
	c.lifetimeMu.Unlock()
	return rows, nil
}

// BySite returns per-site aggregates over a range.
func (c *Collector) BySite(ctx context.Context, from, to time.Time) ([]SiteTraffic, error) {
	rows, err := c.store.TrafficBySite(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]SiteTraffic, 0, len(rows))
	for _, r := range rows {
		t := SiteTraffic{
			SiteID: r.SiteID, Requests: r.RequestCount, Inbound: r.InboundBytes, Outbound: r.OutboundBytes,
			Status2xx: r.Status2xx, Status3xx: r.Status3xx, Status4xx: r.Status4xx, Status5xx: r.Status5xx,
		}
		if r.RequestCount > 0 {
			t.AvgMS = r.ResponseSum / r.RequestCount
		}
		t.P95MS = r.Latency.Percentile(r.RequestCount, 0.95)
		t.P99MS = r.Latency.Percentile(r.RequestCount, 0.99)
		out = append(out, t)
	}
	return out, nil
}

// Rows exposes raw minute rows for advanced queries.
func (c *Collector) Rows(ctx context.Context, siteID uint, from, to time.Time) ([]storage.TrafficStat, error) {
	return c.store.QueryTraffic(ctx, siteID, from, to)
}
