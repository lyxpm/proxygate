package system

import (
	"math"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

// HostStats is a point-in-time host load sample.
type HostStats struct {
	CPUPercent    float64   `json:"cpu_percent"`
	MemoryUsed    uint64    `json:"memory_used"`
	MemoryTotal   uint64    `json:"memory_total"`
	MemoryPercent float64   `json:"memory_percent"`
	SampledAt     time.Time `json:"sampled_at"`
}

// Host samples CPU and memory. CPUPercent(0) is the average since the previous
// call (or boot on first call), so it never blocks the request path.
func Host() HostStats {
	out := HostStats{SampledAt: time.Now()}
	if values, err := cpu.Percent(0, false); err == nil && len(values) > 0 {
		out.CPUPercent = math.Round(values[0]*10) / 10
	}
	if m, err := mem.VirtualMemory(); err == nil {
		out.MemoryUsed = m.Used
		out.MemoryTotal = m.Total
		out.MemoryPercent = math.Round(m.UsedPercent*10) / 10
	}
	return out
}
