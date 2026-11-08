package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/proxygate/proxygate/internal/storage"
)

func TestApplyTimezone(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "tz.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SetSetting(ctx, "timezone", "Asia/Tokyo"); err != nil {
		t.Fatal(err)
	}
	old := time.Local
	defer func() { time.Local = old }()
	applyTimezone(ctx, store, slog.Default())
	if time.Local.String() != "Asia/Tokyo" {
		t.Fatalf("timezone not applied: %s", time.Local)
	}
	if err := store.SetSetting(ctx, "timezone", "Not/AZone"); err != nil {
		t.Fatal(err)
	}
	applyTimezone(ctx, store, slog.Default())
	if time.Local.String() != "Asia/Tokyo" {
		t.Fatal("invalid timezone must keep the previous location")
	}
}
