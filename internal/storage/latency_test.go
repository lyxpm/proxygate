package storage

import "testing"

func TestLatencyHistogramPercentileAndEncoding(t *testing.T) {
	var h LatencyHistogram
	for _, ms := range []int64{1, 5, 10, 20, 50, 100, 200, 500, 1000} {
		h.Add(ms)
	}
	if h.Count() != 9 {
		t.Fatalf("count=%d", h.Count())
	}
	if got := h.Percentile(9, 0.95); got < 1000 || got > 2000 {
		t.Fatalf("p95=%d, want within [1000,2000]", got)
	}
	if got := h.Percentile(9, 0.99); got < 1000 {
		t.Fatalf("p99=%d, want >=1000", got)
	}
	decoded := DecodeLatencyHistogram(h.Encode())
	if decoded != h {
		t.Fatalf("encoded histogram did not round-trip: %v != %v", decoded, h)
	}
}
