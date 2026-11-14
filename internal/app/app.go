// Package app wires configuration, storage, services, the proxy engine and
// the management HTTP server into one process.
package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/proxygate/proxygate/internal/api"
	"github.com/proxygate/proxygate/internal/api/handler"
	"github.com/proxygate/proxygate/internal/application"
	"github.com/proxygate/proxygate/internal/auth"
	"github.com/proxygate/proxygate/internal/certificate"
	"github.com/proxygate/proxygate/internal/config"
	applog "github.com/proxygate/proxygate/internal/log"
	"github.com/proxygate/proxygate/internal/proxy"
	"github.com/proxygate/proxygate/internal/secure"
	"github.com/proxygate/proxygate/internal/site"
	"github.com/proxygate/proxygate/internal/statistics"
	"github.com/proxygate/proxygate/internal/storage"
)

// Version is the application version. It is overridden at build time from the
// release tag via -ldflags "-X .../internal/app.Version=<version>".
var Version = "dev"

// migrateLegacyDatabase moves the pre-ProxyGate database into place before the
// new store opens. The old container must be stopped, so no WAL writer remains.
func migrateLegacyDatabase(cfg config.Config) error {
	newPath := cfg.DataPath("database", "proxygate.db")
	legacy := cfg.DataPath("database", "proxy-manager.db")
	if _, err := os.Stat(newPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat proxygate database: %w", err)
	}
	if _, err := os.Stat(legacy); os.IsNotExist(err) {
		return nil
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		src := legacy + suffix
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("stat legacy database: %w", err)
		}
		if err := os.Rename(src, newPath+suffix); err != nil {
			return fmt.Errorf("migrate legacy database: %w", err)
		}
	}
	return nil
}

// App owns every long-lived component.
type App struct {
	cfg config.Config
	log *slog.Logger

	store        *storage.Store
	engine       *proxy.Engine
	sites        *site.Service
	applications *application.Service
	certs        *certificate.Manager
	stats        *statistics.Collector
	access       *applog.AccessLog
	system       *applog.System
	hub          *applog.Hub
	auth         *auth.Service

	handler     http.Handler
	server      *http.Server
	ln          net.Listener
	httpsServer *http.Server
	httpsLn     net.Listener

	startedAt     time.Time
	version       atomic.Int64
	reloadMu      sync.Mutex
	restartMu     sync.Mutex
	httpsMu       sync.Mutex
	httpsRequired atomic.Bool
	httpsPort     atomic.Int64
	apiCancel     context.CancelFunc
	closeLog      func()
	bgOnce        sync.Once
}

// New builds the application without listening on any socket.
func New(ctx context.Context, cfg config.Config) (*App, error) {
	logger, closeLog, err := newLogger(cfg)
	if err != nil {
		return nil, err
	}
	if err := migrateLegacyDatabase(cfg); err != nil {
		return nil, err
	}
	store, err := storage.Open(cfg.DataPath("database", "proxygate.db"), cfg.Dev)
	if err != nil {
		return nil, err
	}
	ensureSecurityDefaults(ctx, store)
	hub := applog.NewHub()
	retainDays := 14
	if raw, ok, err := store.GetSetting(ctx, "log_retention_days"); err == nil && ok {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 365 {
			retainDays = n
		}
	}
	applyTimezone(ctx, store, logger)
	access := applog.NewAccessLog(cfg.DataDir, hub, retainDays, 512, logger)
	system := applog.NewSystem(store, hub, logger)
	stats := statistics.New(store, logger)

	a := &App{
		cfg: cfg, log: logger, store: store, hub: hub, access: access, system: system, stats: stats,
		startedAt: time.Now(), closeLog: closeLog,
	}
	observer := &observer{access: access, stats: stats, system: system, store: store}
	engine := proxy.New(proxy.Options{Observer: observer, Logger: logger, TrustedProxies: cfg.TrustedProxies})
	if err := engine.Start(ctx); err != nil {
		return nil, err
	}
	a.engine = engine
	if latest, lerr := store.LatestConfigVersion(ctx); lerr == nil {
		a.version.Store(int64(latest.Version))
	}
	a.auth = auth.New(store, cfg.SessionTTL, logger)
	if err := a.auth.ReloadSecuritySettings(ctx); err != nil {
		logger.Warn("load security settings", "error", err)
	}
	box, err := secure.NewBox(cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	a.auth.SetBox(box)
	a.auth.SetPasswordChangedHook(func() {
		if err := os.Remove(filepath.Join(cfg.DataDir, "initial-admin-password.txt")); err != nil && !os.IsNotExist(err) {
			logger.Warn("remove initial password file", "error", err)
		}
	})
	a.certs = certificate.NewManager(store, box, cfg.DataDir, engine.Challenges(), a.Reload, logger)
	a.certs.SetAdminHTTPSReloader(a.reloadAdminHTTPSForCertificate)
	a.certs.ReloadRenewalSettings(ctx)
	acmeClient, err := certificate.NewHTTPClient(cfg.ACMEExtraCAFile)
	if err != nil {
		return nil, err
	}
	a.certs.SetHTTPClient(acmeClient)
	a.applications = application.NewService(store, a.Reload, logger)
	a.applications.SetAllowLoopback(cfg.AllowLoopbackUpstream)
	a.sites = site.NewService(store, a.Reload)
	apiHandler := handler.New(handler.Deps{
		Config: cfg, Store: store, Auth: a.auth, Sites: a.sites, Applications: a.applications,
		Certs: a.certs, Stats: stats, Access: access, System: system, Hub: hub, Engine: engine,
		Reload: a.Reload, Restart: a.Restart, RefreshAdminHTTPS: a.RefreshAdminHTTPS, RestartProcess: a.RestartProcess, Rollback: a.RollbackConfig, StartedAt: a.startedAt, Version: Version, Logger: logger,
	})
	a.handler = api.NewRouter(apiHandler)
	return a, nil
}

// Handler exposes the management HTTP handler (tests, embedding).
func (a *App) Handler() http.Handler { return a.handler }

// Reload rebuilds and atomically applies the proxy runtime from SQLite.
func (a *App) Reload(ctx context.Context) error {
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()

	sites, err := a.store.ListSites(ctx)
	if err != nil {
		return err
	}
	ups, err := a.store.ListApplications(ctx)
	if err != nil {
		return err
	}
	certs, err := a.store.ListCertificates(ctx)
	if err != nil {
		return err
	}
	runtimeApplications := make(map[uint]proxy.ApplicationConfig, len(ups))
	for _, up := range ups {
		uc := proxy.ApplicationConfig{
			ID: up.ID, Name: up.Name, Strategy: up.Strategy, Enabled: up.Enabled,
			Health: proxy.HealthConfig{
				Enabled: up.HealthCheckEnabled, Type: up.HealthCheckType, Path: up.HealthCheckPath,
				Interval:         time.Duration(up.HealthCheckInterval) * time.Second,
				Timeout:          time.Duration(up.HealthCheckTimeout) * time.Second,
				HealthyThreshold: up.HealthyThreshold, UnhealthyThreshold: up.UnhealthyThreshold,
				PassiveFailLimit: up.PassiveFailureLimit,
				PassiveCooldown:  time.Duration(up.PassiveCooldown) * time.Second,
			},
		}
		for _, n := range up.Nodes {
			uc.Nodes = append(uc.Nodes, proxy.NodeConfig{
				ID: n.ID, Address: n.Address, Weight: n.Weight, Priority: n.Priority,
				Enabled: n.Enabled, Healthy: n.Healthy, TLSVerify: n.TLSVerify,
			})
		}
		runtimeApplications[up.ID] = uc
	}
	var runtimeSites []proxy.SiteConfig
	for _, s := range sites {
		if !s.Enabled {
			continue
		}
		opts, err := site.DecodeOptions(s.ExtraConfig)
		if err != nil {
			return fmt.Errorf("site %q has invalid options: %w", s.Name, err)
		}
		sc := proxy.SiteConfig{
			ID: s.ID, Name: s.Name, Enabled: s.Enabled, ListenPort: s.ListenPort,
			Domains: s.Domains, Protocol: s.Protocol, RedirectHTTPS: s.RedirectHTTPS,
			ProxyTimeout: time.Duration(s.ProxyTimeout) * time.Second,
			MaxBodySize:  s.MaxBodySize, ApplicationID: s.ApplicationID, CertificateID: s.CertificateID,
			Options: proxy.SiteOptions{
				StripPrefix: opts.StripPrefix, AddPrefix: opts.AddPrefix,
				UpstreamSNI: opts.UpstreamSNI, UpstreamTLSVerify: boolValue(opts.UpstreamTLSVerify, true),
				KeepAlive: boolValue(opts.KeepAlive, true), UseUpstreamHost: opts.UseUpstreamHost,
				ForwardClientIP: boolValue(opts.ForwardClientIP, true),
				BufferSize:      opts.BufferSize, IPMode: opts.IPMode, IPRules: opts.IPList,
				ConnectionLimitEnabled: opts.ConnectionLimitEnabled, ConnectionLimit: opts.ConnectionLimit,
				RateLimitEnabled: opts.RateLimitEnabled, RateLimitPerSecond: opts.RateLimitPerSecond,
				RateLimitBurst: opts.RateLimitBurst,
			},
		}
		for _, h := range opts.RequestHeaders {
			sc.Options.RequestHeaders = append(sc.Options.RequestHeaders, proxy.Header{Name: h.Name, Value: h.Value})
		}
		for _, h := range opts.ResponseHeaders {
			sc.Options.ResponseHeaders = append(sc.Options.ResponseHeaders, proxy.Header{Name: h.Name, Value: h.Value})
		}
		if s.Protocol == "https" {
			if s.CertificateID == nil {
				return fmt.Errorf("site %q uses HTTPS without a certificate", s.Name)
			}
			cert, err := a.store.GetCertificate(ctx, *s.CertificateID)
			if err != nil {
				return fmt.Errorf("site %q: load certificate: %w", s.Name, err)
			}
			pair, err := a.certs.LoadPair(cert)
			if err != nil {
				return fmt.Errorf("site %q: %w", s.Name, err)
			}
			sc.TLS = pair
		}
		runtimeSites = append(runtimeSites, sc)
	}
	nextVersion := a.version.Load() + 1
	cfg := &proxy.Config{Version: nextVersion, Applications: runtimeApplications, Sites: runtimeSites}
	if err := a.engine.Apply(cfg); err != nil {
		a.system.Logf(ctx, "error", "config", "配置应用失败："+err.Error(), nil)
		return err
	}
	a.version.Store(nextVersion)
	a.saveConfigVersion(ctx, sites, ups, certs)
	a.system.Logf(ctx, "info", "config", fmt.Sprintf("配置已应用（版本 %d，%d 个站点）", cfg.Version, len(runtimeSites)), map[string]any{"version": cfg.Version})
	return nil
}

type configSnapshot struct {
	Sites        []storage.Site        `json:"sites"`
	Applications []storage.Application `json:"applications"`
	Certificates []storage.Certificate `json:"certificates"`
}

// UnmarshalJSON accepts pre-ProxyGate snapshots that used "upstreams" and
// "upstream_id", so older config versions remain rollback-compatible.
func (s *configSnapshot) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if _, ok := raw["applications"]; !ok {
		if legacy, ok := raw["upstreams"]; ok {
			raw["applications"] = legacy
		}
	}
	if sites, ok := raw["sites"]; ok {
		sites = bytes.ReplaceAll(sites, []byte(`"upstream_id"`), []byte(`"application_id"`))
		sites = bytes.ReplaceAll(sites, []byte(`"UpstreamID"`), []byte(`"ApplicationID"`))
		raw["sites"] = sites
	}
	normalized, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	type plain configSnapshot
	var out plain
	if err := json.Unmarshal(normalized, &out); err != nil {
		return err
	}
	*s = configSnapshot(out)
	return nil
}

func (a *App) saveConfigVersion(ctx context.Context, sites []storage.Site, ups []storage.Application, certs []storage.Certificate) {
	raw, err := json.Marshal(configSnapshot{Sites: sites, Applications: ups, Certificates: certs})
	if err != nil {
		a.log.Warn("marshal config snapshot", "error", err)
		return
	}
	sum := sha256.Sum256(raw)
	row := &storage.ConfigVersion{Snapshot: string(raw), Checksum: hex.EncodeToString(sum[:]), Description: "runtime update"}
	if err := a.store.CreateConfigVersion(ctx, row); err != nil {
		a.log.Warn("save config version", "error", err)
		return
	}
	limit := 20
	if raw, ok, err := a.store.GetSetting(ctx, "config_version_limit"); err == nil && ok {
		if n, err := strconv.Atoi(raw); err == nil && n >= 5 && n <= 200 {
			limit = n
		}
	}
	if _, err := a.store.PruneConfigVersions(ctx, limit); err != nil {
		a.log.Warn("prune config versions", "error", err)
	}
}

func ensureSecurityDefaults(ctx context.Context, store *storage.Store) {
	defaults := map[string]string{
		"password_policy_enabled":       "1",
		"password_min_length":           "12",
		"password_require_upper":        "1",
		"password_require_lower":        "1",
		"password_require_digit":        "1",
		"password_require_symbol":       "0",
		"login_max_failures":            "5",
		"login_lock_minutes":            "15",
		"login_lockout_enabled":         "1",
		"session_ttl_minutes":           "1440",
		"session_timeout_enabled":       "1",
		"login_protection_enabled":      "1",
		"session_idle_minutes":          "1440",
		"certificate_renew_before_days": "30",
		"admin_https_enabled":           "0",
		"admin_https_port":              "18443",
		"admin_https_certificate_id":    "0",
	}
	for key, value := range defaults {
		if _, ok, err := store.GetSetting(ctx, key); err == nil && !ok {
			_ = store.SetSetting(ctx, key, value)
		}
	}
}

func boolValue(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// RollbackConfig restores sites and applications from a stored snapshot. If the
// restored configuration cannot be applied, the previous configuration is
// restored and re-applied.
func (a *App) RollbackConfig(ctx context.Context, version int) error {
	row, err := a.store.GetConfigVersion(ctx, version)
	if err != nil {
		return err
	}
	var snap configSnapshot
	if err := json.Unmarshal([]byte(row.Snapshot), &snap); err != nil {
		return fmt.Errorf("parse configuration snapshot: %w", err)
	}
	currentSites, err := a.store.ListSites(ctx)
	if err != nil {
		return err
	}
	currentUps, err := a.store.ListApplications(ctx)
	if err != nil {
		return err
	}
	if err := a.store.ReplaceConfig(ctx, snap.Sites, snap.Applications); err != nil {
		return fmt.Errorf("restore configuration: %w", err)
	}
	if err := a.Reload(ctx); err != nil {
		_ = a.store.ReplaceConfig(ctx, currentSites, currentUps)
		_ = a.Reload(ctx)
		return fmt.Errorf("configuration rollback failed: %w", err)
	}
	a.system.Logf(ctx, "warn", "config", fmt.Sprintf("已回滚到配置版本 %d", version), map[string]any{"version": version})
	return nil
}

func newLogger(cfg config.Config) (*slog.Logger, func(), error) {
	path := filepath.Join(cfg.DataDir, "logs", "system.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, nil, fmt.Errorf("open system log: %w", err)
	}
	w := io.MultiWriter(os.Stderr, f)
	level := slog.LevelInfo
	if cfg.Dev {
		level = slog.LevelDebug
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h), func() { _ = f.Close() }, nil
}
