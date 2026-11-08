package certificate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/proxygate/proxygate/internal/certificate/provider"
	"github.com/proxygate/proxygate/internal/secure"
	"github.com/proxygate/proxygate/internal/storage"
)

func testPair(t *testing.T, host string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host, "*." + host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return string(certPEM), string(keyPEM)
}

func TestDNSProviderCredentialsRoundTrip(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "dns.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	box, _ := secure.NewBox(make([]byte, 32))
	m := NewManager(store, box, t.TempDir(), nil, func(context.Context) error { return nil }, nil)
	p, err := m.CreateDNSProvider(context.Background(), DNSProviderInput{
		Name: "cf", ProviderType: "cloudflare",
		Credentials: provider.Credentials{Secret: "super-secret-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.GetDNSProvider(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.CredentialsEncrypted == "" || row.CredentialsEncrypted == "super-secret-token" {
		t.Fatalf("credentials not encrypted: %q", row.CredentialsEncrypted)
	}
	creds, err := m.dnsCredentials(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if creds.Secret != "super-secret-token" {
		t.Fatalf("round-trip mismatch: %+v", creds)
	}
	if err := m.DeleteDNSProvider(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestWarnExpiringOncePerDay(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "warn.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	box, _ := secure.NewBox(make([]byte, 32))
	m := NewManager(store, box, t.TempDir(), nil, func(context.Context) error { return nil }, nil)
	expires := time.Now().Add(5 * 24 * time.Hour)
	cert := &storage.Certificate{
		Name: "soon", Domains: storage.StringList{"soon.test"}, CertificateType: "acme",
		Status: "valid", ExpiresAt: &expires,
		CertificatePath: "certificates/1/cert.pem", PrivateKeyPath: "certificates/1/private.key",
	}
	if err := store.CreateCertificate(context.Background(), cert); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	m.warnExpiring(context.Background(), now)
	m.warnExpiring(context.Background(), now.Add(time.Hour))
	logs, err := store.ListCertificateLogs(context.Background(), cert.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Level != "warn" {
		t.Fatalf("expected one warning, got %+v", logs)
	}
}

func TestUploadLoadAndDelete(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "cert.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	box, err := secure.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	m := NewManager(store, box, dataDir, nil, func(context.Context) error { return nil }, nil)
	certPEM, keyPEM := testPair(t, "example.test")
	cert, err := m.Upload(context.Background(), UploadRequest{Name: "test", CertificatePEM: certPEM, PrivateKeyPEM: keyPEM})
	if err != nil {
		t.Fatal(err)
	}
	if cert.Status != "valid" || cert.ExpiresAt == nil || cert.CertificatePath == "" {
		t.Fatalf("unexpected record: %+v", cert)
	}
	if _, err := os.Stat(filepath.Join(dataDir, filepath.FromSlash(cert.CertificatePath))); err != nil {
		t.Fatalf("certificate file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, filepath.FromSlash(cert.PrivateKeyPath))); err != nil {
		t.Fatalf("key file missing: %v", err)
	}
	pair, err := m.LoadPair(cert)
	if err != nil || pair.Leaf == nil {
		t.Fatalf("load pair: %v", err)
	}
	if pair.Leaf.Subject.CommonName != "example.test" {
		t.Fatalf("unexpected leaf: %+v", pair.Leaf.Subject)
	}

	// A mismatched key must be rejected before any record is written.
	otherKey := keyPEM
	if certPEM, keyPEM = testPair(t, "other.test"); otherKey == keyPEM {
		t.Fatal("test keys should differ")
	}
	if _, err := m.Upload(context.Background(), UploadRequest{CertificatePEM: certPEM, PrivateKeyPEM: otherKey}); err == nil {
		t.Fatal("expected mismatched key error")
	}

	if err := m.Delete(context.Background(), cert.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(context.Background(), cert.ID); err == nil {
		t.Fatal("certificate should be deleted")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "certificates")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(dataDir, "certificates"))
		if len(entries) != 0 {
			t.Fatalf("certificate directory not cleaned: %v", entries)
		}
	}
}
