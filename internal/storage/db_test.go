package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigrateAndReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range []string{"users", "sessions", "sites", "applications", "application_nodes", "certificates", "traffic_stats", "audit_logs", "system_logs", "config_versions", "certificate_logs"} {
		var name string
		if err := s.DB.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name).Error; err != nil || name != table {
			t.Fatalf("table %s missing: %v %q", table, err, name)
		}
	}
}

func TestApplicationNodesAndSitePreload(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	up := &Application{Name: "pool", Strategy: "round_robin", Enabled: true, HealthCheckEnabled: true,
		HealthCheckType: "http", HealthCheckPath: "/", HealthCheckInterval: 10, HealthCheckTimeout: 3,
		HealthyThreshold: 2, UnhealthyThreshold: 3, PassiveFailureLimit: 3, PassiveCooldown: 30,
		Nodes: []ApplicationNode{{Address: "http://127.0.0.1:9000", Weight: 1, Enabled: true, Healthy: true, TLSVerify: true}},
	}
	if err := s.CreateApplication(ctx, up); err != nil {
		t.Fatal(err)
	}
	if up.ID == 0 || len(up.Nodes) != 1 || up.Nodes[0].ID == 0 || up.Nodes[0].ApplicationID != up.ID {
		t.Fatalf("association not persisted: %+v", up)
	}
	site := &Site{Name: "site", Enabled: true, ListenPort: 80, Domains: StringList{"example.test"}, Protocol: "http",
		ApplicationID: up.ID, ProxyTimeout: 30, ExtraConfig: "{}"}
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Application == nil || loaded.Application.Name != "pool" || len(loaded.Application.Nodes) != 1 {
		t.Fatalf("preload failed: %+v", loaded.Application)
	}
	if len(loaded.Domains) != 1 || loaded.Domains[0] != "example.test" {
		t.Fatalf("domains round-trip failed: %+v", loaded.Domains)
	}
	if err := s.DeleteApplication(ctx, up.ID); err == nil {
		t.Fatal("expected FK error deleting application in use")
	}
}

func TestVacuumIntoSnapshot(t *testing.T) {
	s := testStore(t)
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.DB.Exec("VACUUM INTO ?", path).Error; err != nil {
		t.Fatalf("VACUUM INTO: %v", err)
	}
	snap, err := Open(path, false)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snap.Close()
	if _, err := snap.ListSites(context.Background()); err != nil {
		t.Fatalf("snapshot query: %v", err)
	}
}

func TestConfigVersionPruneAndDelete(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		if err := s.CreateConfigVersion(ctx, &ConfigVersion{Snapshot: "{}", Checksum: "x", Description: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PruneConfigVersions(ctx, 3); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListConfigVersions(ctx, 10, false)
	if err != nil || len(rows) != 3 || rows[0].Version != 6 {
		t.Fatalf("prune result: %v %+v", err, rows)
	}
	if _, err := s.DeleteConfigVersion(ctx, 4); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ListConfigVersions(ctx, 10, false)
	if len(rows) != 2 {
		t.Fatalf("delete did not remove row: %+v", rows)
	}
}

func TestTrafficUpsert(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	bucket := time.Now().UTC().Truncate(time.Minute)
	d := TrafficDelta{SiteID: 1, Bucket: bucket, RequestCount: 2, InboundBytes: 10, OutboundBytes: 20,
		Status2xx: 2, ResponseSum: 30, ResponseMax: 25}
	if err := s.UpsertTraffic(ctx, d); err != nil {
		t.Fatal(err)
	}
	d.RequestCount = 1
	d.OutboundBytes = 5
	d.ResponseSum = 5
	if err := s.UpsertTraffic(ctx, d); err != nil {
		t.Fatal(err)
	}
	rows, err := s.QueryTraffic(ctx, 1, bucket.Add(-time.Minute), bucket.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RequestCount != 3 || rows[0].OutboundBytes != 25 || rows[0].ResponseTimeMax != 25 {
		t.Fatalf("unexpected aggregate: %+v", rows)
	}
}
