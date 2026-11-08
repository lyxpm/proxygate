package applog

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestSeedRecentOnRestart(t *testing.T) {
	dir := t.TempDir()
	first := NewAccessLog(dir, nil, 1, 1, slog.Default())
	now := time.Now()
	first.Record(Entry{Time: now.Add(-2 * time.Second), SiteID: 1, Path: "/one", Status: 200})
	first.Record(Entry{Time: now.Add(-time.Second), SiteID: 1, Path: "/two", Status: 200})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := first.Close(ctx); err != nil {
		t.Fatal(err)
	}

	second := NewAccessLog(dir, nil, 1, 1, slog.Default())
	defer second.Close(context.Background())
	items, total := second.Query(Filter{Page: 1, PageSize: 10})
	if total != 2 || len(items) != 2 || items[0].Path != "/two" {
		t.Fatalf("seeded ring mismatch: total=%d items=%+v", total, items)
	}
}

func TestQueryFile(t *testing.T) {
	a := NewAccessLog(t.TempDir(), nil, 1, 1, slog.Default())
	now := time.Now()
	a.Record(Entry{Time: now.Add(-2 * time.Second), SiteID: 1, ClientIP: "1.1.1.1", Path: "/old", Status: 200})
	a.Record(Entry{Time: now.Add(-time.Second), SiteID: 1, ClientIP: "2.2.2.2", Path: "/new", Status: 500})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	date := now.Format("2006-01-02")
	items, total, err := a.QueryFile(date, Filter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("unexpected result: total=%d items=%v", total, items)
	}
	if items[0].Path != "/new" {
		t.Fatalf("expected newest first, got %q", items[0].Path)
	}
	items, total, err = a.QueryFile(date, Filter{Status: 500, Page: 1, PageSize: 10})
	if err != nil || total != 1 || items[0].Path != "/new" {
		t.Fatalf("status filter failed: %v total=%d items=%v", err, total, items)
	}
	if _, _, err := a.QueryFile("not-a-date", Filter{}); err == nil {
		t.Fatal("expected invalid date error")
	}
}
