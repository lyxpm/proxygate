package auth

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/apperr"
)

// PasswordPolicy is configured from system settings and disabled by default.
type PasswordPolicy struct {
	Enabled       bool
	MinLength     int
	RequireUpper  bool
	RequireLower  bool
	RequireDigit  bool
	RequireSymbol bool
}

// ReloadSecuritySettings applies password policy, login lockout and session
// timeout values from the settings table.
func (s *Service) ReloadSecuritySettings(ctx context.Context) error {
	get := func(key, def string) string {
		if v, ok, err := s.store.GetSetting(ctx, key); err == nil && ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return def
	}
	getInt := func(key string, def, min, max int) int {
		n, err := strconv.Atoi(get(key, strconv.Itoa(def)))
		if err != nil || n < min || n > max {
			return def
		}
		return n
	}
	getBool := func(key string) bool {
		switch strings.ToLower(get(key, "0")) {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	}
	policy := PasswordPolicy{
		Enabled:       getBool("password_policy_enabled"),
		MinLength:     getInt("password_min_length", 8, 8, 64),
		RequireUpper:  getBool("password_require_upper"),
		RequireLower:  getBool("password_require_lower"),
		RequireDigit:  getBool("password_require_digit"),
		RequireSymbol: getBool("password_require_symbol"),
	}
	maxFailures := getInt("login_max_failures", 5, 3, 20)
	lockMinutes := getInt("login_lock_minutes", 15, 1, 1440)
	idleMinutes := getInt("session_idle_minutes", getInt("session_ttl_minutes", 1440, 5, 10080), 5, 10080)
	protectionEnabled := true
	if _, ok, err := s.store.GetSetting(ctx, "login_protection_enabled"); err == nil && ok {
		protectionEnabled = getBool("login_protection_enabled")
	} else {
		// Backward compatibility with the previous two-switch configuration.
		protectionEnabled = getBool("login_lockout_enabled") && getBool("session_timeout_enabled")
	}

	s.settingsMu.Lock()
	s.policy = policy
	s.maxFailures = maxFailures
	s.lockWindow = time.Duration(lockMinutes) * time.Minute
	s.lockoutEnabled = protectionEnabled
	s.sessionTimeoutEnabled = protectionEnabled
	s.ttl = time.Duration(idleMinutes) * time.Minute
	s.settingsMu.Unlock()
	return nil
}

// SessionTTL returns the configured idle timeout. When login protection is
// disabled, a 30-day hard cap still applies to avoid immortal sessions.
func (s *Service) SessionTTL() time.Duration {
	s.settingsMu.RLock()
	enabled := s.sessionTimeoutEnabled
	ttl := s.ttl
	s.settingsMu.RUnlock()
	if !enabled {
		return 30 * 24 * time.Hour
	}
	return ttl
}

// SessionCookieTTL is the browser cookie lifetime; idle expiry is enforced
// server-side so activity can keep extending the session.
func (s *Service) SessionCookieTTL() time.Duration {
	return 30 * 24 * time.Hour
}

// ValidatePassword applies the configured policy. A minimum of eight
// characters always applies; complexity rules only apply when enabled.
func (s *Service) ValidatePassword(password string) error {
	s.settingsMu.RLock()
	p := s.policy
	s.settingsMu.RUnlock()
	if len(password) < 8 {
		return apperr.InvalidField("password", "密码至少 8 位")
	}
	if !p.Enabled {
		return nil
	}
	if len(password) < p.MinLength {
		return apperr.InvalidField("password", "密码长度至少为 "+strconv.Itoa(p.MinLength)+" 位")
	}
	var upper, lower, digit, symbol bool
	for _, r := range password {
		switch {
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			symbol = true
		}
	}
	if p.RequireUpper && !upper {
		return apperr.InvalidField("password", "密码必须包含大写字母")
	}
	if p.RequireLower && !lower {
		return apperr.InvalidField("password", "密码必须包含小写字母")
	}
	if p.RequireDigit && !digit {
		return apperr.InvalidField("password", "密码必须包含数字")
	}
	if p.RequireSymbol && !symbol {
		return apperr.InvalidField("password", "密码必须包含符号")
	}
	return nil
}

func (s *Service) loginPolicy() (int, time.Duration) {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.maxFailures, s.lockWindow
}
