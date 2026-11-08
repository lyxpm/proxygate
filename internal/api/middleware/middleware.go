// Package middleware contains HTTP middleware for the management API.
package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/auth"
	"github.com/proxygate/proxygate/internal/storage"
)

type userCtxKey struct{}
type requestIDCtxKey struct{}
type tokenCtxKey struct{}

// SessionCookie is the management session cookie name.
const SessionCookie = "pg_session"

// CSRFCookie carries the double-submit CSRF token.
const CSRFCookie = "pg_csrf"

// RequestID attaches a request id.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 64 {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDCtxKey{}, id)))
	})
}

// RequestIDFrom returns the request id stored in ctx.
func RequestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(requestIDCtxKey{}).(string)
	return v
}

// Recover turns panics into 500 responses.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic in HTTP handler", "panic", rec, "path", r.URL.Path, "stack", string(debug.Stack()))
					response.JSON(w, http.StatusInternalServerError, 500, "internal server error", nil)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets conservative browser headers for the admin UI.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// Auth resolves the session cookie or bearer token and stores the user.
func Auth(svc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ""
			if c, err := r.Cookie(SessionCookie); err == nil {
				token = c.Value
			}
			if token == "" {
				if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
					token = strings.TrimPrefix(h, "Bearer ")
				}
			}
			if token == "" {
				response.Error(w, apperr.New(apperr.Unauthorized, "authentication required"))
				return
			}
			if strings.HasPrefix(token, "pg_") {
				apiToken, err := svc.AuthenticateAPIToken(r.Context(), token)
				if err != nil {
					response.Error(w, apperr.New(apperr.Unauthorized, "invalid API token"))
					return
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenCtxKey{}, apiToken)))
				return
			}
			user, err := svc.Authenticate(r.Context(), token)
			if err != nil {
				response.Error(w, apperr.New(apperr.Unauthorized, "authentication required"))
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userCtxKey{}, user)))
		})
	}
}

// UserFrom returns the authenticated user.
func UserFrom(ctx context.Context) *storage.User {
	u, _ := ctx.Value(userCtxKey{}).(*storage.User)
	return u
}

// TokenFrom returns the API token used for this request, if any.
func TokenFrom(ctx context.Context) *storage.APIToken {
	t, _ := ctx.Value(tokenCtxKey{}).(*storage.APIToken)
	return t
}

// EnforceTokenScope rejects mutations from read-only API tokens.
func EnforceTokenScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := TokenFrom(r.Context())
		if token == nil {
			next.ServeHTTP(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !auth.HasScope(token, "write") {
			response.Error(w, apperr.New(apperr.Forbidden, "API token is read-only"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// EnforceUserRole makes viewer accounts read-only. Account self-service under
// /auth/ is exempt so viewers can still change their own password or logout.
func EnforceUserRole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFrom(r.Context())
		if user == nil || auth.RoleOf(user) == "admin" {
			next.ServeHTTP(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		response.Error(w, apperr.New(apperr.Forbidden, "当前账号为只读角色"))
	})
}

// RequireAdmin rejects non-administrator sessions.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFrom(r.Context())
		if user == nil || auth.RoleOf(user) != "admin" {
			response.Error(w, apperr.New(apperr.Forbidden, "需要管理员权限"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SessionOnly rejects API-token access for sensitive account management.
func SessionOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if TokenFrom(r.Context()) != nil {
			response.Error(w, apperr.New(apperr.Forbidden, "this endpoint requires a login session"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AllowIP restricts access to the management server to the given IPs/CIDRs.
// An empty list allows every client. It checks the direct peer address; when
// the admin UI is behind a reverse proxy, allow the proxy address.
func AllowIP(raw []string) func(http.Handler) http.Handler {
	var nets []*net.IPNet
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if !strings.Contains(entry, "/") {
			if ip := net.ParseIP(entry); ip != nil {
				if ip.To4() != nil {
					entry += "/32"
				} else {
					entry += "/128"
				}
			}
		}
		_, ipnet, err := net.ParseCIDR(entry)
		if err == nil {
			nets = append(nets, ipnet)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(nets) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			ip := net.ParseIP(host)
			allowed := false
			for _, n := range nets {
				if ip != nil && n.Contains(ip) {
					allowed = true
					break
				}
			}
			if !allowed {
				response.Error(w, apperr.New(apperr.Forbidden, "access denied by admin IP allowlist"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SameOrigin rejects cross-origin state-changing requests.
func SameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
				response.Error(w, apperr.New(apperr.Forbidden, "cross-origin request rejected"))
				return
			}
			if ref := r.Header.Get("Referer"); ref != "" {
				if u, err := url.Parse(ref); err != nil || !strings.EqualFold(u.Host, r.Host) {
					response.Error(w, apperr.New(apperr.Forbidden, "cross-origin request rejected"))
					return
				}
			}
			next.ServeHTTP(w, r)
			return
		}
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			response.Error(w, apperr.New(apperr.Forbidden, "cross-origin request rejected"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CSRF validates the double-submit token for unsafe session-authenticated
// requests. API tokens are exempt because they are not ambient credentials.
func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if TokenFrom(r.Context()) != nil || strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(CSRFCookie)
		header := r.Header.Get("X-CSRF-Token")
		if err != nil || cookie.Value == "" || header == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
			response.Error(w, apperr.New(apperr.Forbidden, "CSRF token missing or invalid"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NoCache prevents browsers from caching API responses.
func NoCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// AccessLogger logs API requests (not proxied traffic).
func AccessLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			status := rec.status
			if status == 0 {
				status = 200
			}
			log.Info("http request", "method", r.Method, "path", r.URL.Path, "status", status, "duration_ms", time.Since(start).Milliseconds(), "ip", r.RemoteAddr)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = 200
	}
	return s.ResponseWriter.Write(p)
}

func (s *statusRecorder) Flush() {
	_ = http.NewResponseController(s.ResponseWriter).Flush()
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
