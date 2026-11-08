package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/auth"
)

// Bootstrap creates the initial administrator and applies the stored runtime
// configuration. It is separated from Run so tests can use Handler directly.
func (a *App) Bootstrap(ctx context.Context) error {
	password, err := a.auth.EnsureAdmin(ctx, a.cfg.AdminPassword)
	if err != nil {
		return fmt.Errorf("bootstrap administrator: %w", err)
	}
	if password != "" {
		path, perr := auth.InitialPasswordFile(a.cfg.DataDir, password)
		if perr != nil {
			a.log.Warn("write initial password file", "error", perr)
		} else {
			a.log.Warn("generated initial administrator password", "username", "admin", "file", path)
		}
	}
	a.bgOnce.Do(func() {
		a.certs.Start(ctx)
		a.stats.Start(ctx)
		go a.cleanupLoop(ctx)
	})
	if err := a.Reload(ctx); err != nil {
		return err
	}
	return nil
}

// Run starts the application and blocks until ctx is cancelled.
func (a *App) Run(ctx context.Context) error {
	if err := a.Bootstrap(ctx); err != nil {
		a.log.Error("initial bootstrap failed; management UI stays available", "error", err)
	}
	a.refreshHTTPSGate(ctx)

	ln, err := net.Listen("tcp", a.cfg.AdminListen)
	if err != nil {
		return fmt.Errorf("listen admin %s: %w", a.cfg.AdminListen, err)
	}
	a.ln = ln
	a.server = &http.Server{
		Handler:           a.adminHTTPHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	serveErr := make(chan error, 1)
	go func() {
		if err := a.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()
	a.log.Info("ProxyGate started", "version", Version, "admin", "http://"+displayAddr(ln.Addr().String()), "data", a.cfg.DataDir)
	a.startAdminHTTPS(ctx)

	select {
	case <-ctx.Done():
		a.log.Info("shutdown signal received")
	case err := <-serveErr:
		a.log.Error("management server stopped", "error", err)
		_ = a.Shutdown(context.Background())
		return err
	}
	return a.Shutdown(context.Background())
}

// refreshHTTPSGate reads the management HTTPS setting before the HTTP server
// starts accepting requests.
func (a *App) refreshHTTPSGate(ctx context.Context) {
	enabled, ok, err := a.store.GetSetting(ctx, "admin_https_enabled")
	if err != nil || !ok || enabled != "1" {
		a.httpsRequired.Store(false)
		return
	}
	a.httpsRequired.Store(true)
	if raw, _, err := a.store.GetSetting(ctx, "admin_https_port"); err == nil {
		if port, err := strconv.Atoi(raw); err == nil && port >= 1024 && port <= 65535 {
			a.httpsPort.Store(int64(port))
		}
	}
}

// adminHTTPHandler keeps HTTP available for health checks and redirects
// browsers/API clients to HTTPS when management HTTPS is enabled. When HTTPS
// is disabled (initial deployment or user opt-out) HTTP remains fully usable.
func (a *App) adminHTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.httpsRequired.Load() || r.URL.Path == "/api/v1/system/health" {
			a.handler.ServeHTTP(w, r)
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host == "" {
			host = "127.0.0.1"
		}
		port := a.httpsPort.Load()
		if port <= 0 {
			port = 18443
		}
		target := "https://" + net.JoinHostPort(host, strconv.FormatInt(port, 10)) + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}

func (a *App) startAdminHTTPS(ctx context.Context) {
	a.httpsMu.Lock()
	defer a.httpsMu.Unlock()
	enabled, ok, err := a.store.GetSetting(ctx, "admin_https_enabled")
	if err != nil || !ok || enabled != "1" {
		a.httpsRequired.Store(false)
		return
	}
	a.httpsRequired.Store(true)
	if a.httpsServer != nil {
		return
	}
	portRaw, _, _ := a.store.GetSetting(ctx, "admin_https_port")
	port, err := strconv.Atoi(portRaw)
	if err != nil || port < 1024 || port > 65535 {
		a.log.Warn("admin HTTPS disabled: invalid port", "value", portRaw)
		return
	}
	a.httpsPort.Store(int64(port))
	certRaw, _, _ := a.store.GetSetting(ctx, "admin_https_certificate_id")
	certID, err := strconv.ParseUint(certRaw, 10, 64)
	if err != nil || certID == 0 {
		a.log.Warn("admin HTTPS disabled: no certificate selected")
		return
	}
	cert, err := a.certs.Get(ctx, uint(certID))
	if err != nil {
		a.log.Warn("admin HTTPS disabled: certificate not found", "id", certID, "error", err)
		return
	}
	pair, err := a.certs.LoadPair(cert)
	if err != nil {
		a.log.Warn("admin HTTPS disabled: cannot load certificate", "id", certID, "error", err)
		return
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		a.log.Warn("admin HTTPS disabled: listen failed", "port", port, "error", err)
		return
	}
	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{*pair},
		NextProtos:   []string{"h2", "http/1.1"},
	}
	srv := &http.Server{
		Handler:           a.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		TLSConfig:         tlsCfg,
	}
	a.httpsLn = tls.NewListener(ln, tlsCfg)
	a.httpsServer = srv
	go func() {
		if err := srv.Serve(a.httpsLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.log.Warn("admin HTTPS server stopped", "error", err)
		}
	}()
	a.log.Info("admin HTTPS started", "address", fmt.Sprintf("https://%s", displayAddr(ln.Addr().String())), "certificate", cert.Name)
}

// stopAdminHTTPS closes the management HTTPS listener, if any.
func (a *App) stopAdminHTTPS(ctx context.Context) {
	a.httpsMu.Lock()
	defer a.httpsMu.Unlock()
	if a.httpsServer == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.httpsServer.Shutdown(shutdownCtx); err != nil {
		a.log.Warn("shutdown admin HTTPS server", "error", err)
		_ = a.httpsServer.Close()
	}
	a.httpsServer = nil
	a.httpsLn = nil
}

// Restart reapplies proxy configuration and restarts the management HTTPS
// listener so settings such as admin_https_enabled take effect immediately.
// The management HTTP listener stays available during the restart.
func (a *App) Restart(ctx context.Context) error {
	a.restartMu.Lock()
	defer a.restartMu.Unlock()
	a.log.Info("restart requested")

	a.stopAdminHTTPS(ctx)
	if a.engine == nil {
		return fmt.Errorf("proxy engine is not initialized")
	}
	if err := a.engine.Stop(ctx); err != nil {
		a.log.Warn("stop proxy engine for restart", "error", err)
	}
	if err := a.engine.Start(context.Background()); err != nil {
		return fmt.Errorf("start proxy engine: %w", err)
	}
	if err := a.Reload(ctx); err != nil {
		return fmt.Errorf("reload proxy configuration: %w", err)
	}
	a.startAdminHTTPS(ctx)
	a.log.Info("restart completed")
	return nil
}

// reloadAdminHTTPSForCertificate refreshes the management HTTPS listener only
// when the changed certificate is the one configured for management HTTPS.
// It is delayed so the API response can be written first.
func (a *App) reloadAdminHTTPSForCertificate(ctx context.Context, certificateID uint) {
	raw, ok, err := a.store.GetSetting(ctx, "admin_https_certificate_id")
	if err != nil || !ok {
		return
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || uint(id) != certificateID {
		return
	}
	go func() {
		time.Sleep(time.Second)
		rctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		a.stopAdminHTTPS(rctx)
		a.startAdminHTTPS(rctx)
		a.log.Info("admin HTTPS certificate reloaded", "certificate", certificateID)
	}()
}

// RestartProcess exits the process after the restore response has been sent.
// Docker's restart policy starts a new process, which applies the staged
// restore before opening the database.
func (a *App) RestartProcess() {
	if a.server == nil {
		// Tests and embedded handlers have no managed process to restart.
		return
	}
	go func() {
		time.Sleep(time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = a.Shutdown(ctx)
		cancel()
		os.Exit(0)
	}()
}

// Shutdown stops listeners, the proxy, background writers and the database.
func (a *App) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.ShutdownTimeout)
	defer cancel()
	if a.server != nil {
		if err := a.server.Shutdown(ctx); err != nil {
			a.log.Warn("shutdown management server", "error", err)
			_ = a.server.Close()
		}
	}
	if a.httpsServer != nil {
		if err := a.httpsServer.Shutdown(ctx); err != nil {
			a.log.Warn("shutdown management HTTPS server", "error", err)
			_ = a.httpsServer.Close()
		}
	}
	if a.engine != nil {
		if err := a.engine.Stop(ctx); err != nil {
			a.log.Warn("stop proxy engine", "error", err)
		}
	}
	if a.access != nil {
		if err := a.access.Close(ctx); err != nil {
			a.log.Warn("close access log", "error", err)
		}
	}
	if a.stats != nil {
		a.stats.Wait(ctx)
	}
	if a.system != nil {
		a.system.Close(ctx)
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			a.log.Warn("close database", "error", err)
		}
	}
	if a.closeLog != nil {
		a.closeLog()
	}
	return nil
}

func (a *App) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.auth.CleanupSessions(ctx)
			a.auth.CleanupAttempts()
			now := time.Now()
			if _, err := a.store.DeleteSystemLogsBefore(ctx, now.AddDate(0, 0, -30)); err != nil {
				a.log.Warn("purge system logs", "error", err)
			}
			if _, err := a.store.DeleteAuditBefore(ctx, now.AddDate(0, -6, 0)); err != nil {
				a.log.Warn("purge audit logs", "error", err)
			}
			a.stats.Purge(ctx, now.AddDate(0, 0, -90))
		}
	}
}

func displayAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
