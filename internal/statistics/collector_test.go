package statistics

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/proxygate/proxygate/internal/proxy"
	"github.com/proxygate/proxygate/internal/storage"
)

func TestCollectorFlushAndTotals(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "stats.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c := New(store, nil)
	now := time.Now()
	c.Record(proxy.RequestEvent{Time: now, SiteID: 1, Status: 200, Duration: 40 * time.Millisecond, RequestBytes: 10, ResponseBytes: 100})
	c.Record(proxy.RequestEvent{Time: now, SiteID: 1, Status: 502, Duration: 10 * time.Millisecond, RequestBytes: 5, ResponseBytes: 0})
	c.Record(proxy.RequestEvent{Time: now, SiteID: 2, Status: 301, Duration: 20 * time.Millisecond, RequestBytes: 1, ResponseBytes: 2})
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	totals, err := c.Totals(context.Background(), now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if totals.Requests != 3 || totals.Inbound != 16 || totals.Outbound != 102 {
		t.Fatalf("unexpected totals: %+v", totals)
	}
	if totals.Status2xx != 1 || totals.Status3xx != 1 || totals.Status5xx != 1 {
		t.Fatalf("unexpected status counts: %+v", totals)
	}
	if totals.AvgMS != 23 {
		t.Fatalf("avg=%d, want 23", totals.AvgMS)
	}
	bySite, err := c.BySite(context.Background(), now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(bySite) != 2 {
		t.Fatalf("expected 2 sites, got %+v", bySite)
	}
	p95, p99 := c.Percentiles()
	if p95 == 0 || p99 == 0 {
		t.Fatalf("percentiles should be populated, got %d/%d", p95, p99)
	}
}
