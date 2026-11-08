package applog

import (
	"sort"
	"strings"
	"time"
)

// TopItem is one aggregated value (client IP or URL path).
type TopItem struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// Top aggregates the in-memory access log by client IP and URL path for a site
// and time range. It returns at most limit items each, sorted by count.
func (a *AccessLog) Top(siteID uint, from, to time.Time, limit int) ([]TopItem, []TopItem) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	ips := map[string]int64{}
	paths := map[string]int64{}
	a.mu.RLock()
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
		if siteID != 0 && e.SiteID != siteID {
			continue
		}
		if !from.IsZero() && e.Time.Before(from) {
			continue
		}
		if !to.IsZero() && !e.Time.Before(to) {
			continue
		}
		if e.ClientIP != "" {
			ips[e.ClientIP]++
		}
		path := e.Path
		if i := strings.IndexByte(path, '?'); i >= 0 {
			path = path[:i]
		}
		if path != "" {
			paths[path]++
		}
	}
	a.mu.RUnlock()
	return topN(ips, limit), topN(paths, limit)
}

func topN(m map[string]int64, limit int) []TopItem {
	out := make([]TopItem, 0, len(m))
	for value, count := range m {
		out = append(out, TopItem{Value: value, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
