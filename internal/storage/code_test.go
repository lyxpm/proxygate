package storage

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
)

func TestBusinessCodesAndAuditBackfill(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "codes.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	app := &Application{Name: "code-app", Strategy: "round_robin"}
	if err := store.CreateApplication(ctx, app); err != nil {
		t.Fatal(err)
	}
	site := &Site{Name: "code-site", ListenPort: 18080, Domains: StringList{"code.test"}, Protocol: "http", ApplicationID: app.ID}
	if err := store.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	if site.Code == "" {
		t.Fatal("site code was not generated")
	}
	audit := &AuditLog{
		Action: "create_site", ResourceType: "site", ResourceID: strconv.Itoa(int(site.ID)),
		Result: "success",
	}
	if err := store.InsertAudit(ctx, audit); err != nil {
		t.Fatal(err)
	}
	if err := store.backfillAuditResourceCodes(); err != nil {
		t.Fatal(err)
	}
	var stored AuditLog
	if err := store.DB.First(&stored, audit.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ResourceID != site.Code {
		t.Fatalf("audit resource = %q, want %q", stored.ResourceID, site.Code)
	}
}
