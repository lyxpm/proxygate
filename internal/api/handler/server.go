// Package handler implements the /api/v1 endpoints. Handlers validate input,
// call services and map results; business rules stay in the service packages.
package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/proxygate/proxygate/internal/api/middleware"
	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/application"
	"github.com/proxygate/proxygate/internal/auth"
	"github.com/proxygate/proxygate/internal/certificate"
	"github.com/proxygate/proxygate/internal/config"
	applog "github.com/proxygate/proxygate/internal/log"
	"github.com/proxygate/proxygate/internal/proxy"
	"github.com/proxygate/proxygate/internal/site"
	"github.com/proxygate/proxygate/internal/statistics"
	"github.com/proxygate/proxygate/internal/storage"
)

// Deps are the services used by HTTP handlers.
type Deps struct {
	Config         config.Config
	Store          *storage.Store
	Auth           *auth.Service
	Sites          *site.Service
	Applications   *application.Service
	Certs          *certificate.Manager
	Stats          *statistics.Collector
	Access         *applog.AccessLog
	System         *applog.System
	Hub            *applog.Hub
	Engine         *proxy.Engine
	Reload         func(context.Context) error
	Restart        func(context.Context) error
	RestartProcess func()
	Rollback       func(context.Context, int) error
	StartedAt      time.Time
	Version        string
	Logger         *slog.Logger
}

// Server holds handler dependencies.
type Server struct {
	Deps
	sse atomic.Int64
}

// New creates the API server.
func New(d Deps) *Server {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Server{Deps: d}
}

func (s *Server) audit(r *http.Request, action, resource string, id any, result string, detail map[string]any) {
	uid := uint(0)
	if u := userFrom(r); u != nil {
		uid = u.ID
	}
	s.auditForUser(r, uid, action, resource, id, result, detail)
}

func (s *Server) auditForUser(r *http.Request, uid uint, action, resource string, id any, result string, detail map[string]any) {
	raw := ""
	if len(detail) > 0 {
		if b, err := json.Marshal(detail); err == nil {
			raw = string(b)
		}
	}
	row := &storage.AuditLog{
		UserID: &uid, Action: action, ResourceType: resource, ResourceID: toString(id),
		ClientIP: clientIP(r), Result: result, Detail: raw,
	}
	if err := s.Store.InsertAudit(r.Context(), row); err != nil {
		s.Logger.Warn("write audit log", "error", err)
	}
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if apperr.KindOf(err) == apperr.Internal {
		s.Logger.Error("request failed", "path", r.URL.Path, "error", err)
	}
	responseError(w, err)
}

// verifyReauth confirms the current administrator's password for a sensitive
// operation, with a per-user/IP failure limit.
func (s *Server) verifyReauth(w http.ResponseWriter, r *http.Request, password string) bool {
	u := userFrom(r)
	if u == nil {
		s.fail(w, r, apperr.New(apperr.Unauthorized, "not authenticated"))
		return false
	}
	ip := clientIP(r)
	if s.Auth.ReauthBlocked(u.ID, ip) {
		s.fail(w, r, apperr.New(apperr.TooMany, "密码确认失败次数过多，请稍后再试"))
		return false
	}
	if err := s.Auth.VerifyPassword(r.Context(), u.ID, password); err != nil {
		s.Auth.ReauthFailed(u.ID, ip)
		s.fail(w, r, err)
		return false
	}
	s.Auth.ReauthSucceeded(u.ID, ip)
	return true
}

func parseID(r *http.Request, key string) (uint, error) {
	raw := chi.URLParam(r, key)
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 {
		return 0, apperr.Invalidf("invalid %s", key)
	}
	return uint(n), nil
}

func userFrom(r *http.Request) *storage.User { return middleware.UserFrom(r.Context()) }

func responseError(w http.ResponseWriter, err error) { response.Error(w, err) }

func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case int:
		return strconv.Itoa(x)
	default:
		return ""
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func rangeParams(r *http.Request) (from, to time.Time, step time.Duration, err error) {
	to = time.Now()
	key := strings.TrimSpace(r.URL.Query().Get("range"))
	if key == "" {
		key = "24h"
	}
	var window time.Duration
	switch key {
	case "1h":
		window, step = time.Hour, time.Minute
	case "6h":
		window, step = 6*time.Hour, 5*time.Minute
	case "24h":
		window, step = 24*time.Hour, 15*time.Minute
	case "7d":
		window, step = 7*24*time.Hour, time.Hour
	case "30d":
		window, step = 30*24*time.Hour, 6*time.Hour
	default:
		return time.Time{}, time.Time{}, 0, apperr.Invalidf("range must be 1h, 6h, 24h, 7d or 30d")
	}
	return to.Add(-window), to, step, nil
}

func boolQuery(r *http.Request, key string) *bool {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil
	}
	v := raw == "1" || strings.EqualFold(raw, "true")
	return &v
}
