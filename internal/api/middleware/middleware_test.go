package middleware

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/proxygate/proxygate/internal/storage"
)

func TestViewerReadOnly(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := EnforceUserRole(next)
	req := func(method string) *http.Request {
		r := httptest.NewRequest(method, "/api/v1/sites", nil)
		return r.WithContext(context.WithValue(r.Context(), userCtxKey{}, &storage.User{ID: 2, Role: "viewer"}))
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req(http.MethodGet))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("viewer GET = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req(http.MethodPost))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer POST = %d, want 403", rec.Code)
	}
}

func TestRequireAdmin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := RequireAdmin(next)
	mk := func(role string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
		return r.WithContext(context.WithValue(r.Context(), userCtxKey{}, &storage.User{ID: 2, Role: role}))
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, mk("viewer"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer admin route = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, mk("admin"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin route = %d", rec.Code)
	}
}

func TestSecurityHeadersDoNotLockAdminToHTTPS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	SecurityHeaders(next).ServeHTTP(rec, req)
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("management HSTS must stay disabled, got %q", got)
	}
}

func TestAllowIP(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	handler := AllowIP([]string{"127.0.0.1", "10.0.0.0/8"})(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || !called {
		t.Fatalf("allowed request rejected: %d", rec.Code)
	}

	called = false
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.10:1234"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || called {
		t.Fatalf("disallowed request not rejected: %d", rec.Code)
	}

	// Empty allowlist permits everyone.
	open := AllowIP(nil)(next)
	called = false
	rec = httptest.NewRecorder()
	open.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || !called {
		t.Fatalf("empty allowlist should allow: %d", rec.Code)
	}

	called = false
	invalid := AllowIP([]string{"not-a-cidr"})(next)
	rec = httptest.NewRecorder()
	invalid.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || called {
		t.Fatalf("invalid allowlist should fail closed: %d", rec.Code)
	}
}
