// Package api wires the REST routes and the embedded SPA.
package api

import (
	"compress/gzip"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/proxygate/proxygate/internal/api/handler"
	"github.com/proxygate/proxygate/internal/api/middleware"
	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/web"
)

// NewRouter builds the management API and static frontend handler.
func NewRouter(s *handler.Server) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.AllowIP(s.Config.AdminAllowCIDRs))
	r.Use(middleware.RequestID)
	r.Use(middleware.Recover(s.Logger))
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.AccessLogger(s.Logger))

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", s.Login)
		r.Get("/system/health", s.Health)
		r.Get("/system/version", s.VersionInfo)

		r.Group(func(r chi.Router) {
			r.Use(middleware.Auth(s.Auth))
			r.Use(middleware.SameOrigin)
			r.Use(middleware.CSRF)
			r.Use(middleware.NoCache)
			r.Use(middleware.EnforceTokenScope)
			r.Use(middleware.EnforceUserRole)

			r.Group(func(r chi.Router) {
				r.Use(middleware.SessionOnly)
				r.Post("/auth/logout", s.Logout)
				r.Get("/auth/me", s.Me)
				r.Put("/auth/password", s.ChangePassword)
				r.Get("/auth/totp", s.TOTPStatus)
				r.Post("/auth/totp/setup", s.SetupTOTP)
				r.Post("/auth/totp/enable", s.EnableTOTP)
				r.Post("/auth/totp/disable", s.DisableTOTP)
				r.Get("/auth/sessions", s.ListSessions)
				r.Post("/auth/sessions/revoke-others", s.RevokeOtherSessions)
				r.Delete("/auth/sessions/{id}", s.RevokeSession)
				r.With(middleware.RequireAdmin).Post("/system/restart", s.RestartEngine)
				r.With(middleware.RequireAdmin).Get("/api-tokens", s.ListAPITokens)
				r.With(middleware.RequireAdmin).Post("/api-tokens", s.CreateAPIToken)
				r.With(middleware.RequireAdmin).Delete("/api-tokens/{id}", s.DeleteAPIToken)
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireAdmin)
					r.Get("/users", s.ListUsers)
					r.Post("/users", s.CreateUser)
					r.Put("/users/{id}", s.UpdateUser)
					r.Delete("/users/{id}", s.DeleteUser)
				})
			})

			r.Get("/dashboard/overview", s.DashboardOverview)
			r.Get("/dashboard/traffic", s.DashboardTraffic)
			r.Get("/dashboard/status", s.DashboardStatus)
			r.Get("/dashboard/events", s.DashboardEvents)

			r.Get("/sites", s.ListSites)
			r.Post("/sites", s.CreateSite)
			r.Post("/sites/import", s.ImportSite)
			r.Get("/sites/{code}", s.GetSite)
			r.Put("/sites/{code}", s.UpdateSite)
			r.Delete("/sites/{code}", s.DeleteSite)
			r.Post("/sites/{code}/enable", s.EnableSite)
			r.Post("/sites/{code}/disable", s.DisableSite)
			r.Get("/sites/{code}/statistics", s.SiteStatistics)
			r.Get("/sites/{code}/logs", s.SiteLogs)
			r.With(middleware.SessionOnly, middleware.RequireAdmin).Get("/sites/{code}/export", s.ExportSite)

			r.Get("/applications", s.ListApplications)
			r.Post("/applications", s.CreateApplication)
			r.Get("/applications/{code}", s.GetApplication)
			r.Put("/applications/{code}", s.UpdateApplication)
			r.Delete("/applications/{code}", s.DeleteApplication)
			r.Get("/applications/{code}/health", s.ApplicationHealth)
			r.Post("/applications/{code}/nodes", s.AddNode)
			r.Put("/applications/{code}/nodes/{node_id}", s.UpdateNode)
			r.Delete("/applications/{code}/nodes/{node_id}", s.DeleteNode)

			r.Get("/certificates", s.ListCertificates)
			r.Post("/certificates/acme", s.CreateACMECertificate)
			r.Post("/certificates/upload", s.UploadCertificate)
			r.Get("/certificates/{code}", s.GetCertificate)
			r.Get("/certificates/{code}/detail", s.GetCertificateDetail)
			r.Get("/certificates/{code}/download", s.DownloadCertificate)
			r.With(middleware.SessionOnly).Post("/certificates/{code}/export", s.ExportCertificateKey)
			r.Delete("/certificates/{code}", s.DeleteCertificate)
			r.Post("/certificates/{code}/renew", s.RenewCertificate)
			r.Get("/certificates/{code}/logs", s.CertificateLogs)

			r.Get("/dns-providers", s.ListDNSProviders)
			r.Post("/dns-providers", s.CreateDNSProvider)
			r.Put("/dns-providers/{id}", s.UpdateDNSProvider)
			r.Delete("/dns-providers/{id}", s.DeleteDNSProvider)

			r.Get("/acme-accounts", s.ListACMEAccounts)
			r.Delete("/acme-accounts/{id}", s.DeleteACMEAccount)

			r.Get("/logs/access", s.AccessLogs)
			r.Get("/logs/system", s.SystemLogs)
			r.Get("/logs/audit", s.AuditLogs)
			r.Get("/logs/stream", s.Stream)
			r.With(middleware.SessionOnly, middleware.RequireAdmin).Get("/logs/access/download", s.DownloadAccessLog)
			r.Get("/events/stream", s.Stream)

			r.Get("/statistics/overview", s.StatisticsOverview)
			r.Get("/statistics/traffic", s.StatisticsTraffic)
			r.Get("/statistics/sites", s.StatisticsSites)
			r.Get("/statistics/status-codes", s.StatisticsStatusCodes)
			r.Get("/statistics/applications", s.StatisticsApplications)

			r.Get("/topology", s.Topology)

			r.Get("/system/info", s.Info)
			r.Get("/system/metrics", s.Metrics)
			r.Get("/system/settings", s.Settings)
			r.With(middleware.SessionOnly).Put("/system/settings", s.UpdateSettings)
			r.Get("/system/config-versions", s.ConfigVersions)
			r.With(middleware.SessionOnly).Post("/system/config-versions/batch-delete", s.BatchDeleteConfigVersions)
			r.With(middleware.SessionOnly).Post("/system/config-versions/{version}/rollback", s.RollbackConfig)
			r.With(middleware.SessionOnly).Delete("/system/config-versions/{version}", s.DeleteConfigVersion)
			r.With(middleware.SessionOnly).Post("/system/backup", s.Backup)
			r.With(middleware.SessionOnly).Post("/system/restore", s.Restore)
		})
	})

	r.NotFound(spaHandler)
	return r
}

// spaHandler serves the embedded Vue build, falling back to index.html for
// client-side routes. Unknown /api paths get a JSON 404 instead of HTML.
func spaHandler(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		response.Error(w, apperr.NotFoundf("endpoint not found"))
		return
	}
	dist, err := fs.Sub(web.FS, "dist")
	if err != nil {
		http.Error(w, "frontend not built", http.StatusNotFound)
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" || p == "." {
		p = "index.html"
	}
	data, err := fs.ReadFile(dist, p)
	if err != nil {
		data, err = fs.ReadFile(dist, "index.html")
		if err != nil {
			http.Error(w, "frontend not built", http.StatusNotFound)
			return
		}
		p = "index.html"
	}
	switch {
	case strings.HasSuffix(p, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case strings.HasSuffix(p, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(p, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(p, ".json"):
		w.Header().Set("Content-Type", "application/json")
	case strings.HasSuffix(p, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	if strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	compressible := strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".css") ||
		strings.HasSuffix(p, ".svg") || strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".html")
	if compressible && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Vary", "Accept-Encoding")
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write(data)
		_ = gz.Close()
		return
	}
	_, _ = w.Write(data)
}
