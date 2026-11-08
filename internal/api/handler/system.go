package handler

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/auth"
	"github.com/proxygate/proxygate/internal/system"
	"gorm.io/gorm"
)

// Restart reapplies proxy/admin HTTPS configuration after password re-auth.
// The response is sent before the listener restart so the request can complete.
func (s *Server) RestartEngine(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil || auth.RoleOf(u) != "admin" {
		s.fail(w, r, apperr.New(apperr.Forbidden, "需要管理员权限"))
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.verifyReauth(w, r, in.Password) {
		s.audit(r, "restart_engine", "system", nil, "failure", nil)
		return
	}
	if s.Restart == nil {
		s.fail(w, r, apperr.New(apperr.Internal, "restart is not available"))
		return
	}
	s.audit(r, "restart_engine", "system", nil, "success", nil)
	response.JSON(w, http.StatusAccepted, 0, "restarting", map[string]any{"restarting": true})
	go func() {
		time.Sleep(300 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.Restart(ctx); err != nil {
			s.Logger.Error("restart failed", "error", err)
		}
	}()
}

// Info returns build and runtime information.
func (s *Server) Info(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]any{
		"version":            s.Version,
		"go_version":         runtime.Version(),
		"os":                 runtime.GOOS,
		"arch":               runtime.GOARCH,
		"uptime_seconds":     int(time.Since(s.StartedAt).Seconds()),
		"started_at":         s.StartedAt,
		"engine":             s.Engine.Status(),
		"host":               system.Host(),
		"access_log_dropped": s.Access.Dropped(),
	})
}

// Health is a public liveness/readiness probe.
func (s *Server) Health(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	code := http.StatusOK
	dbOK := true
	if sqlDB, err := s.Store.DB.DB(); err != nil || sqlDB.PingContext(r.Context()) != nil {
		dbOK = false
	}
	if !dbOK || !s.Engine.Status().Running {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	response.JSON(w, code, 0, status, map[string]any{
		"status": status, "database": dbOK, "proxy": s.Engine.Status().Running, "time": time.Now(),
	})
}

// Metrics exposes low-cardinality Prometheus metrics.
func (s *Server) Metrics(w http.ResponseWriter, r *http.Request) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	status := s.Engine.Status()
	reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "pg_proxy_active_connections", Help: "Current proxied connections."}, func() float64 {
		return float64(s.Engine.Status().ActiveConnections)
	}))
	reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "pg_proxy_config_version", Help: "Applied runtime configuration version."}, func() float64 {
		return float64(s.Engine.Status().ConfigVersion)
	}))
	nodeGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "pg_application_node_healthy", Help: "Application node health (1 healthy, 0 unhealthy)."}, []string{"application", "node"})
	nodeActive := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "pg_application_node_active_connections", Help: "In-flight requests per application node."}, []string{"application", "node"})
	nodeRequests := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "pg_application_node_requests", Help: "Requests served by an application node since process start."}, []string{"application", "node"})
	for _, up := range status.Applications {
		for _, n := range up.Nodes {
			v := 0.0
			if n.Healthy {
				v = 1
			}
			nodeGauge.WithLabelValues(up.Name, n.Address).Set(v)
			nodeActive.WithLabelValues(up.Name, n.Address).Set(float64(n.Active))
			nodeRequests.WithLabelValues(up.Name, n.Address).Set(float64(n.Requests))
		}
	}
	reg.MustRegister(nodeGauge, nodeActive, nodeRequests)

	if s.Stats != nil {
		rows, err := s.Stats.LifetimeBySite(r.Context(), time.Now())
		if err == nil {
			names := map[uint]string{}
			if sites, err := s.Sites.List(r.Context()); err == nil {
				for _, site := range sites {
					names[site.ID] = site.Name
				}
			}
			var requests, inbound, outbound, s2, s3, s4, s5 int64
			siteReq := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "pg_site_requests", Help: "Requests per site in retained traffic stats."}, []string{"site"})
			siteOut := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "pg_site_outbound_bytes", Help: "Outbound bytes per site in retained traffic stats."}, []string{"site"})
			for _, row := range rows {
				name := names[row.SiteID]
				if name == "" {
					name = fmt.Sprintf("site-%d", row.SiteID)
				}
				siteReq.WithLabelValues(name).Set(float64(row.RequestCount))
				siteOut.WithLabelValues(name).Set(float64(row.OutboundBytes))
				requests += row.RequestCount
				inbound += row.InboundBytes
				outbound += row.OutboundBytes
				s2 += row.Status2xx
				s3 += row.Status3xx
				s4 += row.Status4xx
				s5 += row.Status5xx
			}
			reg.MustRegister(siteReq, siteOut)
			reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "pg_requests", Help: "Requests in retained traffic stats."}, func() float64 { return float64(requests) }))
			reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "pg_inbound_bytes", Help: "Inbound bytes in retained traffic stats."}, func() float64 { return float64(inbound) }))
			reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "pg_outbound_bytes", Help: "Outbound bytes in retained traffic stats."}, func() float64 { return float64(outbound) }))
			statusGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "pg_responses", Help: "Responses by status class in retained traffic stats."}, []string{"class"})
			statusGauge.WithLabelValues("2xx").Set(float64(s2))
			statusGauge.WithLabelValues("3xx").Set(float64(s3))
			statusGauge.WithLabelValues("4xx").Set(float64(s4))
			statusGauge.WithLabelValues("5xx").Set(float64(s5))
			reg.MustRegister(statusGauge)
			p95, p99 := s.Stats.Percentiles()
			reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "pg_request_duration_p95_milliseconds", Help: "P95 request duration over recent requests."}, func() float64 { return float64(p95) }))
			reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "pg_request_duration_p99_milliseconds", Help: "P99 request duration over recent requests."}, func() float64 { return float64(p99) }))
		}
	}
	h := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}

// Settings returns system settings.
func (s *Server) Settings(w http.ResponseWriter, r *http.Request) {
	values, err := s.Store.AllSettings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, map[string]any{"values": values})
}

// UpdateSettings validates and stores allowed settings.
func (s *Server) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	allowed := map[string]func(string) error{
		"log_retention_days": func(v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 365 {
				return apperr.Invalidf("log_retention_days must be between 1 and 365")
			}
			return nil
		},
		"timezone": func(v string) error {
			if v == "" {
				return nil
			}
			if _, err := time.LoadLocation(v); err != nil {
				return apperr.Invalidf("unknown timezone %q", v)
			}
			return nil
		},
		"config_version_limit": func(v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 5 || n > 200 {
				return apperr.Invalidf("config_version_limit must be between 5 and 200")
			}
			return nil
		},
		"password_policy_enabled": func(v string) error {
			if v != "0" && v != "1" {
				return apperr.Invalidf("password_policy_enabled must be 0 or 1")
			}
			return nil
		},
		"password_min_length": func(v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 8 || n > 64 {
				return apperr.Invalidf("password_min_length must be between 8 and 64")
			}
			return nil
		},
		"password_require_upper":  boolSetting,
		"password_require_lower":  boolSetting,
		"password_require_digit":  boolSetting,
		"password_require_symbol": boolSetting,
		"login_max_failures": func(v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 3 || n > 20 {
				return apperr.Invalidf("login_max_failures must be between 3 and 20")
			}
			return nil
		},
		"login_lock_minutes": func(v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 1440 {
				return apperr.Invalidf("login_lock_minutes must be between 1 and 1440")
			}
			return nil
		},
		"login_lockout_enabled":    func(v string) error { return boolSetting(v) },
		"session_timeout_enabled":  func(v string) error { return boolSetting(v) },
		"login_protection_enabled": func(v string) error { return boolSetting(v) },
		"session_idle_minutes": func(v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 5 || n > 10080 {
				return apperr.Invalidf("session_idle_minutes must be between 5 and 10080")
			}
			return nil
		},
		"certificate_renew_before_days": func(v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 7 || n > 60 {
				return apperr.Invalidf("certificate_renew_before_days must be between 7 and 60")
			}
			return nil
		},
		"session_ttl_minutes": func(v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 5 || n > 10080 {
				return apperr.Invalidf("session_ttl_minutes must be between 5 and 10080")
			}
			return nil
		},
		"admin_https_enabled": func(v string) error { return boolSetting(v) },
		"admin_https_port": func(v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1024 || n > 65535 {
				return apperr.Invalidf("admin_https_port must be between 1024 and 65535")
			}
			return nil
		},
		"admin_https_certificate_id": func(v string) error {
			if _, err := strconv.Atoi(v); err != nil {
				return apperr.Invalidf("admin_https_certificate_id must be a number")
			}
			return nil
		},
	}
	for k, v := range in {
		validate, ok := allowed[k]
		if !ok {
			s.fail(w, r, apperr.Invalidf("unknown setting %q", k))
			return
		}
		if err := validate(v); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	for k, v := range in {
		if err := s.Store.SetSetting(r.Context(), k, v); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if raw, ok := in["config_version_limit"]; ok {
		if n, err := strconv.Atoi(raw); err == nil {
			_, _ = s.Store.PruneConfigVersions(r.Context(), n)
		}
	}
	if err := s.Auth.ReloadSecuritySettings(r.Context()); err != nil {
		s.Logger.Warn("reload security settings", "error", err)
	}
	if s.Certs != nil {
		s.Certs.ReloadRenewalSettings(r.Context())
	}
	adminHTTPSChanged := false
	for _, key := range []string{"admin_https_enabled", "admin_https_port", "admin_https_certificate_id"} {
		if _, ok := in[key]; ok {
			adminHTTPSChanged = true
			break
		}
	}
	s.audit(r, "update_settings", "system", nil, "success", map[string]any{"keys": keysOf(in)})
	values, _ := s.Store.AllSettings(r.Context())
	response.OK(w, map[string]any{"values": values})
	if adminHTTPSChanged && s.RefreshAdminHTTPS != nil {
		// Let the response complete first: disabling HTTPS from an HTTPS page
		// would otherwise close the listener while the request is still active.
		go func() {
			time.Sleep(500 * time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s.RefreshAdminHTTPS(ctx)
		}()
	}
}

func boolSetting(v string) error {
	if v != "0" && v != "1" {
		return apperr.Invalidf("boolean setting must be 0 or 1")
	}
	return nil
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// sanitizeBackupDB removes operational logs, traffic statistics and sessions
// from the configuration snapshot. Site/application/certificate/settings data
// and their certificate files remain restorable.
func sanitizeBackupDB(path string) error {
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(path)+"?mode=rw"), &gorm.Config{})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	for _, table := range []string{"audit_logs", "system_logs", "certificate_logs", "traffic_stats", "sessions", "config_versions"} {
		if err := db.Exec("DELETE FROM " + table).Error; err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	if err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		return fmt.Errorf("checkpoint backup database: %w", err)
	}
	return db.Exec("VACUUM").Error
}

// Backup streams a zip of the database, certificates, ACME accounts and config.
func (s *Server) Backup(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		s.fail(w, r, apperr.New(apperr.Unauthorized, "not authenticated"))
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.verifyReauth(w, r, in.Password) {
		s.audit(r, "backup", "system", nil, "failure", nil)
		return
	}
	// VACUUM INTO creates a consistent snapshot even while WAL is active.
	snapshot := filepath.Join(s.Config.DataDir, "tmp", fmt.Sprintf("backup-%d.db", time.Now().UnixNano()))
	if err := s.Store.DB.Exec("VACUUM INTO ?", snapshot).Error; err != nil {
		s.fail(w, r, apperr.Internalf(err, "create database snapshot"))
		return
	}
	defer func() {
		_ = os.Remove(snapshot)
		_ = os.Remove(snapshot + "-wal")
		_ = os.Remove(snapshot + "-shm")
	}()
	if err := sanitizeBackupDB(snapshot); err != nil {
		s.fail(w, r, apperr.Internalf(err, "sanitize database snapshot"))
		return
	}

	name := fmt.Sprintf("proxygate-%s.zip", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	for _, dir := range []string{"database", "certificates", "acme", "config"} {
		root := filepath.Join(s.Config.DataDir, dir)
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			rel, err := filepath.Rel(s.Config.DataDir, path)
			if err != nil {
				return nil
			}
			switch filepath.ToSlash(rel) {
			case "database/proxygate.db-wal", "database/proxygate.db-shm", "database/proxygate.db-journal", "config/restore-pending.zip":
				return nil
			}
			source := path
			if filepath.ToSlash(rel) == "database/proxygate.db" {
				source = snapshot
			}
			f, err := zw.Create(filepath.ToSlash(rel))
			if err != nil {
				return err
			}
			src, err := os.Open(source)
			if err != nil {
				return nil
			}
			_, _ = io.Copy(f, src)
			_ = src.Close()
			return nil
		})
		if err != nil {
			s.Logger.Warn("backup walk", "dir", dir, "error", err)
		}
	}
	s.audit(r, "backup", "system", nil, "success", nil)
}

// Restore stages an uploaded backup for the next process start.
func (s *Server) Restore(w http.ResponseWriter, r *http.Request) {
	if !s.verifyReauth(w, r, r.Header.Get("X-Admin-Password")) {
		s.audit(r, "restore", "system", nil, "failure", nil)
		return
	}
	const limit = 512 << 20
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := system.ScheduleRestore(s.Config.DataDir, r.Body, limit); err != nil {
		s.audit(r, "restore", "system", nil, "failure", nil)
		s.fail(w, r, apperr.Invalidf("invalid backup: %v", err))
		return
	}
	s.audit(r, "restore", "system", nil, "success", nil)
	response.JSON(w, http.StatusAccepted, 0, "accepted", map[string]any{"restart_required": true, "restarting": true})
	if s.RestartProcess != nil {
		s.RestartProcess()
	}
}

// ConfigVersions lists stored configuration snapshots.
func (s *Server) ConfigVersions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.ListConfigVersions(r.Context(), 200, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, map[string]any{"items": rows})
}

// RollbackConfig restores a previous configuration snapshot.
func (s *Server) RollbackConfig(w http.ResponseWriter, r *http.Request) {
	if s.Rollback == nil {
		s.fail(w, r, apperr.New(apperr.Unavailable, "rollback is not available"))
		return
	}
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version <= 0 {
		s.fail(w, r, apperr.Invalidf("invalid version"))
		return
	}
	if err := s.Rollback(r.Context(), version); err != nil {
		s.audit(r, "rollback_config", "config", version, "failure", nil)
		s.fail(w, r, apperr.Internalf(err, "configuration rollback failed"))
		return
	}
	s.audit(r, "rollback_config", "config", version, "success", nil)
	response.OK(w, map[string]any{"version": version})
}

// DeleteConfigVersion removes an old snapshot after password confirmation.
func (s *Server) DeleteConfigVersion(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		s.fail(w, r, apperr.New(apperr.Unauthorized, "not authenticated"))
		return
	}
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version <= 0 {
		s.fail(w, r, apperr.Invalidf("invalid version"))
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.verifyReauth(w, r, in.Password) {
		s.audit(r, "delete_config_version", "config", version, "failure", nil)
		return
	}
	latest, err := s.Store.LatestConfigVersion(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if latest.Version == version {
		s.fail(w, r, apperr.Invalidf("当前配置版本不可删除"))
		return
	}
	rows, err := s.Store.DeleteConfigVersion(r.Context(), version)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if rows == 0 {
		s.fail(w, r, apperr.NotFoundf("configuration version not found"))
		return
	}
	s.audit(r, "delete_config_version", "config", version, "success", nil)
	response.OK(w, map[string]any{"version": version})
}

// BatchDeleteConfigVersions removes multiple old snapshots after password confirmation.
func (s *Server) BatchDeleteConfigVersions(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		s.fail(w, r, apperr.New(apperr.Unauthorized, "not authenticated"))
		return
	}
	var in struct {
		Versions []int  `json:"versions"`
		Password string `json:"password"`
	}
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.verifyReauth(w, r, in.Password) {
		s.audit(r, "delete_config_version", "config", nil, "failure", map[string]any{"count": len(in.Versions)})
		return
	}
	latest, err := s.Store.LatestConfigVersion(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	versions := make([]int, 0, len(in.Versions))
	for _, v := range in.Versions {
		if v > 0 && v != latest.Version {
			versions = append(versions, v)
		}
	}
	if len(versions) == 0 {
		s.fail(w, r, apperr.Invalidf("没有可删除的配置版本"))
		return
	}
	rows, err := s.Store.DeleteConfigVersions(r.Context(), versions)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "delete_config_version", "config", nil, "success", map[string]any{"count": rows, "versions": versions})
	response.OK(w, map[string]any{"deleted": rows, "versions": versions})
}

// Version returns the version string.
func (s *Server) VersionInfo(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]string{"version": s.Version})
}
