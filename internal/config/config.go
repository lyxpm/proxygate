// Package config loads process-level configuration from environment variables.
// Business configuration (sites, applications, certificates) lives in SQLite,
// not here: this package only covers bootstrap and deployment concerns.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/system"
)

// Config is the immutable process configuration.
type Config struct {
	DataDir               string
	AdminListen           string
	SessionTTL            time.Duration
	LogLevel              string
	MasterKey             []byte
	TrustedProxies        []string
	AdminAllowCIDRs       []string
	Dev                   bool
	AdminPassword         string
	PublicURL             string
	ACMEExtraCAFile       string
	AllowLoopbackUpstream bool
	ShutdownTimeout       time.Duration
}

// Load reads configuration from the environment, creating the data directory
// layout and a master key on first start.
func Load() (Config, error) {
	c := Config{
		DataDir:               env("PG_DATA_DIR", "./data"),
		AdminListen:           env("PG_ADMIN_LISTEN", "127.0.0.1:8080"),
		LogLevel:              env("PG_LOG_LEVEL", "info"),
		SessionTTL:            24 * time.Hour,
		Dev:                   envBool("PG_DEV", false),
		AdminPassword:         os.Getenv("PG_ADMIN_PASSWORD"),
		PublicURL:             strings.TrimRight(os.Getenv("PG_PUBLIC_URL"), "/"),
		ACMEExtraCAFile:       os.Getenv("PG_ACME_CA_FILE"),
		AllowLoopbackUpstream: envBool("PG_ALLOW_LOOPBACK_UPSTREAM", false),
		ShutdownTimeout:       15 * time.Second,
	}
	if v := os.Getenv("PG_SESSION_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute {
			return Config{}, fmt.Errorf("invalid PG_SESSION_TTL %q", v)
		}
		c.SessionTTL = d
	}
	if v := os.Getenv("PG_TRUSTED_PROXIES"); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				c.TrustedProxies = append(c.TrustedProxies, p)
			}
		}
	}
	if v := os.Getenv("PG_ADMIN_ALLOW_CIDRS"); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				c.AdminAllowCIDRs = append(c.AdminAllowCIDRs, p)
			}
		}
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve data dir: %w", err)
	}
	c.DataDir = abs
	for _, dir := range []string{"", "database", "certificates", "acme", "logs", "logs/access", "config", "backups", "tmp"} {
		if err := os.MkdirAll(filepath.Join(c.DataDir, dir), 0o750); err != nil {
			return Config{}, fmt.Errorf("create data dir %q: %w", dir, err)
		}
	}
	if err := system.ApplyPendingRestore(c.DataDir); err != nil {
		return Config{}, fmt.Errorf("apply pending restore: %w", err)
	}
	if c.MasterKey, err = loadMasterKey(c.DataDir); err != nil {
		return Config{}, err
	}
	return c, nil
}

// DataPath joins a relative path under the data directory.
func (c Config) DataPath(elem ...string) string {
	parts := append([]string{c.DataDir}, elem...)
	return filepath.Join(parts...)
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// loadMasterKey reads config/master.key or creates a new 32-byte key.
// The key protects DNS credentials and other secrets at rest.
func loadMasterKey(dataDir string) ([]byte, error) {
	path := filepath.Join(dataDir, "config", "master.key")
	if v := strings.TrimSpace(os.Getenv("PG_MASTER_KEY")); v != "" {
		key, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(key) != 32 {
			return nil, errors.New("PG_MASTER_KEY must be base64 encoded 32 bytes")
		}
		return key, nil
	}
	if data, err := os.ReadFile(path); err == nil {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("invalid master key file %s", path)
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	return key, nil
}
