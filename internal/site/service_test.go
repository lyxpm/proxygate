package site

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/proxygate/proxygate/internal/storage"
)

func newSiteFixture(t *testing.T) (*Service, *storage.Store, uint) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "site.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	up := &storage.Application{Name: "pool", Strategy: "round_robin", Enabled: true, HealthCheckEnabled: true,
		HealthCheckType: "http", HealthCheckPath: "/", HealthCheckInterval: 10, HealthCheckTimeout: 3,
		HealthyThreshold: 2, UnhealthyThreshold: 3, PassiveFailureLimit: 3, PassiveCooldown: 30,
		Nodes: []storage.ApplicationNode{{Address: "http://127.0.0.1:9000", Weight: 1, Enabled: true, Healthy: true, TLSVerify: true}},
	}
	if err := store.CreateApplication(context.Background(), up); err != nil {
		t.Fatal(err)
	}
	return NewService(store, func(context.Context) error { return nil }), store, up.ID
}

func TestSiteValidationAndConflicts(t *testing.T) {
	svc, store, upID := newSiteFixture(t)
	ctx := context.Background()
	base := Input{Name: "site-a", ListenPort: 18080, Domains: []string{"Example.TEST"}, Protocol: "http", ApplicationID: upID}
	created, err := svc.Create(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if created.Domains[0] != "example.test" {
		t.Fatalf("domain not normalised: %v", created.Domains)
	}
	if _, err := svc.Create(ctx, Input{Name: "dup", ListenPort: 18080, Domains: []string{"example.test"}, Protocol: "http", ApplicationID: upID}); err == nil {
		t.Fatal("expected exact domain conflict")
	}
	if _, err := svc.Create(ctx, Input{Name: "bad", ListenPort: 18081, Domains: []string{"bad_domain"}, Protocol: "http", ApplicationID: upID}); err == nil {
		t.Fatal("expected invalid domain error")
	}
	if _, err := svc.Create(ctx, Input{Name: "no-up", ListenPort: 18081, Domains: []string{"x.test"}, Protocol: "http", ApplicationID: 999}); err == nil {
		t.Fatal("expected missing application error")
	}
	if _, err := svc.Create(ctx, Input{Name: "https-no-cert", ListenPort: 18443, Domains: []string{"s.test"}, Protocol: "https", ApplicationID: upID}); err == nil {
		t.Fatal("expected HTTPS without certificate error")
	}

	cert := &storage.Certificate{Name: "cert", Domains: storage.StringList{"s.test"}, CertificateType: "upload",
		Status: "valid", CertificatePath: "certificates/1/cert.pem", PrivateKeyPath: "certificates/1/private.key"}
	exp := time.Now().Add(60 * 24 * time.Hour)
	cert.ExpiresAt = &exp
	if err := store.CreateCertificate(ctx, cert); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, Input{Name: "https-ok", ListenPort: 18443, Domains: []string{"s.test"}, Protocol: "https", ApplicationID: upID, CertificateID: &cert.ID}); err != nil {
		t.Fatalf("https site with certificate failed: %v", err)
	}
	if _, err := svc.Create(ctx, Input{Name: "mixed", ListenPort: 18080, Domains: []string{"m.test"}, Protocol: "https", ApplicationID: upID, CertificateID: &cert.ID}); err == nil {
		t.Fatal("expected protocol mismatch on the same port")
	}
	disabled := false
	if _, err := svc.SetEnabled(ctx, created.ID, disabled); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
}

func TestOptionsDefaults(t *testing.T) {
	opts, err := DecodeOptions("{}")
	if err != nil {
		t.Fatal(err)
	}
	if opts.KeepAlive == nil || !*opts.KeepAlive || opts.UpstreamTLSVerify == nil || !*opts.UpstreamTLSVerify {
		t.Fatalf("defaults not applied: %+v", opts)
	}
	if opts.ForwardClientIP == nil || !*opts.ForwardClientIP {
		t.Fatalf("forward client IP default not applied: %+v", opts)
	}
	if opts.BufferSize != 32*1024 {
		t.Fatalf("buffer size default %d", opts.BufferSize)
	}
}

func TestOptionsIPAccessControl(t *testing.T) {
	opts, err := normalizeOptions(Options{IPMode: "allow", IPList: []string{"127.0.0.1", "10.0.0.0/8", "2001:db8::/32"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.IPList) != 3 || opts.IPList[0] != "127.0.0.1/32" {
		t.Fatalf("IP list not normalised: %+v", opts.IPList)
	}
	if _, err := normalizeOptions(Options{IPMode: "allow"}); err == nil {
		t.Fatal("allow mode without entries must fail")
	}
	if _, err := normalizeOptions(Options{IPMode: "allow", IPList: []string{"not-an-ip"}}); err == nil {
		t.Fatal("invalid CIDR must fail")
	}
	if _, err := normalizeOptions(Options{IPMode: "block", IPList: []string{"127.0.0.1"}}); err == nil {
		t.Fatal("invalid mode must fail")
	}
}
