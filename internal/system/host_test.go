package system

import "testing"

func TestHostStats(t *testing.T) {
	h := Host()
	if h.CPUPercent < 0 || h.CPUPercent > 100 {
		t.Fatalf("cpu percent out of range: %v", h.CPUPercent)
	}
	if h.MemoryTotal == 0 || h.MemoryUsed == 0 {
		t.Fatalf("memory stats missing: %+v", h)
	}
	if h.MemoryPercent < 0 || h.MemoryPercent > 100 {
		t.Fatalf("memory percent out of range: %v", h.MemoryPercent)
	}
	if h.SampledAt.IsZero() {
		t.Fatal("sampled_at missing")
	}
}
