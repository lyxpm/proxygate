package applog

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestAccessLogTop(t *testing.T) {
	a := NewAccessLog(t.TempDir(), nil, 1, 1, slog.Default())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = a.Close(ctx)
	}()
	now := time.Now()
	a.Record(Entry{Time: now, SiteID: 1, ClientIP: "1.1.1.1", Path: "/a?x=1", Status: 200})
	a.Record(Entry{Time: now, SiteID: 1, ClientIP: "2.2.2.2", Path: "/b", Status: 200})
	a.Record(Entry{Time: now, SiteID: 1, ClientIP: "1.1.1.1", Path: "/a", Status: 200})
	a.Record(Entry{Time: now, SiteID: 2, ClientIP: "3.3.3.3", Path: "/c", Status: 200})

	ips, paths := a.Top(1, now.Add(-time.Minute), now.Add(time.Minute), 10)
	if len(ips) != 2 || ips[0].Value != "1.1.1.1" || ips[0].Count != 2 {
		t.Fatalf("unexpected top ips: %+v", ips)
	}
	if len(paths) != 2 || paths[0].Value != "/a" || paths[0].Count != 2 {
		t.Fatalf("unexpected top paths: %+v", paths)
	}

	allIPs, allPaths := a.Top(0, now.Add(-time.Minute), now.Add(time.Minute), 10)
	if len(allIPs) != 3 {
		t.Fatalf("expected 3 IPs across sites, got %+v", allIPs)
	}
	if len(allPaths) != 3 || allPaths[0].Count != 2 {
		t.Fatalf("unexpected global paths: %+v", allPaths)
	}

	// Range filtering excludes everything.
	old, _ := a.Top(1, now.Add(-2*time.Hour), now.Add(-time.Hour), 10)
	if len(old) != 0 {
		t.Fatalf("expected empty range, got %+v", old)
	}
}
