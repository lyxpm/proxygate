package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminHTTPHandlerHTTPSGate(t *testing.T) {
	a := &App{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})}
	a.httpsPort.Store(18443)

	// HTTPS disabled: the initial deployment and opt-out path stays usable.
	a.httpsRequired.Store(false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://admin.test:18080/login", nil)
	req.Host = "admin.test:18080"
	a.adminHTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("http disabled: status=%d", rec.Code)
	}

	// HTTPS enabled: health remains available, everything else redirects.
	a.httpsRequired.Store(true)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "http://admin.test:18080/api/v1/system/health", nil)
	req.Host = "admin.test:18080"
	a.adminHTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "http://admin.test:18080/api/v1/auth/login?x=1", nil)
	req.Host = "admin.test:18080"
	a.adminHTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("redirect status=%d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://admin.test:18443/api/v1/auth/login?x=1" {
		t.Fatalf("redirect location=%q", loc)
	}
}
