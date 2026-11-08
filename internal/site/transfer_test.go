package site

import (
	"context"
	"testing"
)

func TestExportImport(t *testing.T) {
	svc, _, upID := newSiteFixture(t)
	ctx := context.Background()
	created, err := svc.Create(ctx, Input{
		Name: "original", ListenPort: 18080, Domains: []string{"export.test"},
		Protocol: "http", ApplicationID: upID,
	})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := svc.Export(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exported.ApplicationName != "pool" || exported.Domains[0] != "export.test" {
		t.Fatalf("unexpected export: %+v", exported)
	}
	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	imported, err := svc.Import(ctx, ImportInput{
		Name: "imported", ListenPort: exported.ListenPort, Domains: exported.Domains,
		Protocol: exported.Protocol, ApplicationName: exported.ApplicationName,
		ProxyTimeout: exported.ProxyTimeout, Options: exported.Options,
	})
	if err != nil {
		t.Fatal(err)
	}
	if imported.Name != "imported" || imported.ApplicationID != upID {
		t.Fatalf("unexpected imported site: %+v", imported)
	}
	if _, err := svc.Export(ctx, imported.ID); err != nil {
		t.Fatal(err)
	}
}
