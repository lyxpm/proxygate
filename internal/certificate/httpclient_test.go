package certificate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewHTTPClientTrustBundle(t *testing.T) {
	certPEM, _ := testPair(t, "acme.test")
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, []byte(certPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewHTTPClient(path)
	if err != nil || client == nil {
		t.Fatalf("build client: %v", err)
	}
	if _, err := NewHTTPClient(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("expected missing CA file error")
	}
	if _, err := NewHTTPClient(""); err != nil {
		t.Fatalf("empty CA file should use system roots: %v", err)
	}
}
