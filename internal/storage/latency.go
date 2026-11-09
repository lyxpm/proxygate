package storage

import (
	"math"
	"strconv"
	"strings"
)

// LatencyBounds are the inclusive upper bounds, in milliseconds, for the
// fixed latency histogram. The last bucket is >= 10s.
var LatencyBounds = [...]int64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 30000}

// LatencyHistogram is a compact fixed-bucket histogram used to calculate
// range and per-minute P95/P99 without storing individual request samples.
type LatencyHistogram [len(LatencyBounds)]int64

// Add records one duration in milliseconds.
func (h *LatencyHistogram) Add(ms int64) {
	if ms < 0 {
		ms = 0
	}
	for i, bound := range LatencyBounds {
		if ms < bound {
			h[i]++
			return
		}
	}
	h[len(h)-1]++
}

// AddHistogram merges another histogram.
func (h *LatencyHistogram) AddHistogram(other LatencyHistogram) {
	for i := range h {
		h[i] += other[i]
	}
}

// Count returns the number of recorded samples.
func (h LatencyHistogram) Count() int64 {
	var total int64
	for _, n := range h {
		total += n
	}
	return total
}

// Percentile returns an interpolated percentile in milliseconds.
func (h LatencyHistogram) Percentile(requests int64, quantile float64) int64 {
	count := h.Count()
	if count == 0 || requests <= 0 {
		return 0
	}
	if requests < count {
		count = requests
	}
	target := int64(math.Ceil(float64(count) * quantile))
	if target < 1 {
		target = 1
	}
	var cumulative int64
	lower := int64(0)
	for i, n := range h {
		upper := LatencyBounds[i]
		if cumulative+n >= target {
			if n == 0 {
				return upper
			}
			fraction := float64(target-cumulative) / float64(n)
			return int64(math.Round(float64(lower) + fraction*float64(upper-lower)))
		}
		cumulative += n
		lower = upper
	}
	return lower
}

// Encode serializes the histogram into a compact comma-separated string.
func (h LatencyHistogram) Encode() string {
	parts := make([]string, len(h))
	for i, n := range h {
		parts[i] = strconv.FormatInt(n, 10)
	}
	return strings.Join(parts, ",")
}

// DecodeLatencyHistogram parses an encoded histogram. Empty or invalid
// histograms are treated as zeroes so old rows remain readable.
func DecodeLatencyHistogram(raw string) LatencyHistogram {
	var h LatencyHistogram
	if strings.TrimSpace(raw) == "" {
		return h
	}
	for i, part := range strings.Split(raw, ",") {
		if i >= len(h) {
			break
		}
		n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err == nil && n > 0 {
			h[i] = n
		}
	}
	return h
}
