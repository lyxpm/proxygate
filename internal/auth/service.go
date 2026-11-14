// Package auth implements administrator login, sessions and password changes.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/secure"
	"github.com/proxygate/proxygate/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

const (
	maxFailures = 5
	failWindow  = 15 * time.Minute
)

type attempt struct {
	count int
	until time.Time
}

// Service implements authentication.
type Service struct {
	store *storage.Store
	ttl   time.Duration
	log   *slog.Logger
	box   *secure.Box

	mu              sync.Mutex
	attempts        map[string]*attempt
	reauth          map[string]*attempt
	passwordChanged func()

	settingsMu            sync.RWMutex
	policy                PasswordPolicy
	maxFailures           int
	lockWindow            time.Duration
	lockoutEnabled        bool
	sessionTimeoutEnabled bool
}

// New creates an auth service.
func New(store *storage.Store, ttl time.Duration, log *slog.Logger) *Service {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: store, ttl: ttl, log: log, attempts: make(map[string]*attempt), reauth: make(map[string]*attempt),
		maxFailures: maxFailures, lockWindow: failWindow, lockoutEnabled: true, sessionTimeoutEnabled: true}
}

// SetPasswordChangedHook registers cleanup that runs after a password change.
func (s *Service) SetPasswordChangedHook(fn func()) { s.passwordChanged = fn }

// SetBox attaches the secret box used to encrypt TOTP seeds.
func (s *Service) SetBox(box *secure.Box) { s.box = box }

// TOTPEnabled reports whether two-factor authentication is active.
func (s *Service) TOTPEnabled(ctx context.Context, userID uint) (bool, error) {
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return false, err
	}
	return user != nil && user.TOTPEnabled, nil
}

// SetupTOTP creates a pending secret. It does not enable 2FA until the user
// confirms a valid code.
func (s *Service) SetupTOTP(ctx context.Context, userID uint) (string, string, error) {
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if user.TOTPEnabled {
		return "", "", apperr.Invalidf("两步验证已启用")
	}
	if s.box == nil {
		return "", "", errors.New("auth: TOTP encryption is not configured")
	}
	secret, err := generateTOTPSecret()
	if err != nil {
		return "", "", err
	}
	enc, err := s.box.Seal([]byte(secret), fmt.Sprintf("user-totp:%d", userID))
	if err != nil {
		return "", "", err
	}
	user.TOTPSecret = enc
	user.TOTPEnabled = false
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return "", "", err
	}
	return secret, totpURL("Proxy Manager", user.Username, secret), nil
}

// EnableTOTP verifies a pending seed and enables 2FA.
func (s *Service) EnableTOTP(ctx context.Context, userID uint, code string) error {
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if user.TOTPEnabled {
		return nil
	}
	if s.box == nil || user.TOTPSecret == "" {
		return apperr.Invalidf("请先生成两步验证密钥")
	}
	raw, err := s.box.Open(user.TOTPSecret, fmt.Sprintf("user-totp:%d", userID))
	if err != nil {
		return err
	}
	if !validateTOTP(string(raw), code, time.Now()) {
		return apperr.Invalidf("动态验证码不正确")
	}
	user.TOTPEnabled = true
	user.LastTOTPStep = 0
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return err
	}
	return nil
}

// DisableTOTP verifies the password and removes 2FA.
func (s *Service) DisableTOTP(ctx context.Context, userID uint, password string) error {
	if err := s.VerifyPassword(ctx, userID, password); err != nil {
		return err
	}
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	user.TOTPEnabled = false
	user.TOTPSecret = ""
	user.LastTOTPStep = 0
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return err
	}
	return nil
}

// EnsureAdmin creates the initial administrator when the user table is empty.
// It returns a generated password (empty when a password was supplied).
func (s *Service) EnsureAdmin(ctx context.Context, password string) (string, error) {
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return "", err
	}
	if n > 0 {
		return "", nil
	}
	generated := false
	if strings.TrimSpace(password) == "" {
		pw, err := secure.RandomPassword(18)
		if err != nil {
			return "", err
		}
		password = pw
		generated = true
	}
	if len(password) < 8 {
		return "", errors.New("initial admin password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", err
	}
	admin := &storage.User{Username: "admin", PasswordHash: string(hash), Enabled: true, Role: "admin"}
	if err := s.store.CreateUser(ctx, admin); err != nil {
		return "", err
	}
	if generated {
		s.log.Warn("initial administrator created with a generated password; change it after login", "username", "admin")
		return password, nil
	}
	s.log.Info("initial administrator created", "username", "admin")
	return "", nil
}

// Login verifies credentials and creates a session token.
func (s *Service) Login(ctx context.Context, username, password, clientIP, userAgent string, totpCode ...string) (*storage.User, string, error) {
	code := ""
	if len(totpCode) > 0 {
		code = totpCode[0]
	}
	username = strings.TrimSpace(username)
	key := username + "|" + clientIP
	if s.blocked(key) {
		return nil, "", apperr.New(apperr.Forbidden, "too many failed attempts; try again later")
	}
	user, err := s.store.GetUserByUsername(ctx, username)
	if err != nil || user == nil || !user.Enabled {
		s.fail(key)
		return nil, "", apperr.New(apperr.Unauthorized, "invalid username or password")
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		s.fail(key)
		return nil, "", apperr.New(apperr.Unauthorized, "invalid username or password")
	}
	if user.TOTPEnabled {
		if s.box == nil || user.TOTPSecret == "" {
			s.fail(key)
			return nil, "", apperr.New(apperr.Unauthorized, "两步验证配置异常")
		}
		if strings.TrimSpace(code) == "" {
			return nil, "", apperr.New(apperr.Unauthorized, "请输入动态验证码")
		}
		raw, err := s.box.Open(user.TOTPSecret, fmt.Sprintf("user-totp:%d", user.ID))
		if err != nil {
			s.fail(key)
			return nil, "", apperr.New(apperr.Unauthorized, "两步验证配置异常")
		}
		step, ok := validateTOTPStep(string(raw), code, time.Now())
		if !ok {
			s.fail(key)
			return nil, "", apperr.New(apperr.Unauthorized, "动态验证码不正确")
		}
		claimed, err := s.store.ClaimTOTPStep(ctx, user.ID, step)
		if err != nil || !claimed {
			s.fail(key)
			return nil, "", apperr.New(apperr.Unauthorized, "动态验证码已使用，请等待下一次刷新")
		}
		user.LastTOTPStep = step
	}
	s.reset(key)
	token, err := secure.RandomToken(32)
	if err != nil {
		return nil, "", err
	}
	now := time.Now()
	user.LastLoginAt = &now
	if err := s.store.UpdateUser(ctx, user); err != nil {
		s.log.Warn("update last login", "error", err)
	}
	sess := &storage.Session{
		TokenHash: hashToken(token), UserID: user.ID,
		ExpiresAt: now.Add(30 * 24 * time.Hour), ClientIP: clientIP, UserAgent: truncate(userAgent, 250),
		LastSeenAt: &now,
	}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		return nil, "", err
	}
	return user, token, nil
}

// Authenticate resolves a raw session token to a user.
func (s *Service) Authenticate(ctx context.Context, token string) (*storage.User, error) {
	if token == "" {
		return nil, apperr.New(apperr.Unauthorized, "not authenticated")
	}
	sess, err := s.store.GetSession(ctx, hashToken(token))
	if err != nil {
		return nil, apperr.New(apperr.Unauthorized, "session expired")
	}
	now := time.Now()
	s.settingsMu.RLock()
	idleEnabled := s.sessionTimeoutEnabled
	idleTTL := s.ttl
	s.settingsMu.RUnlock()
	lastSeen := sess.CreatedAt
	if sess.LastSeenAt != nil {
		lastSeen = *sess.LastSeenAt
	}
	if idleEnabled && !lastSeen.IsZero() && now.Sub(lastSeen) > idleTTL {
		_ = s.store.DeleteSession(ctx, hashToken(token))
		return nil, apperr.New(apperr.Unauthorized, "session idle timeout")
	}
	if now.Sub(lastSeen) > time.Minute {
		_ = s.store.TouchSession(ctx, hashToken(token), now)
	}
	user, err := s.store.GetUser(ctx, sess.UserID)
	if err != nil || !user.Enabled {
		return nil, apperr.New(apperr.Unauthorized, "account disabled")
	}
	return user, nil
}

// Logout deletes a session.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, hashToken(token))
}

// VerifyPassword checks the current administrator password without creating a
// session. Used for destructive operations that need a second confirmation.
func (s *Service) VerifyPassword(ctx context.Context, userID uint, password string) error {
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return apperr.Invalidf("管理员密码不正确")
	}
	return nil
}

// ChangePassword verifies the old password and updates the hash.
func (s *Service) ChangePassword(ctx context.Context, userID uint, oldPassword, newPassword string) error {
	if err := s.ValidatePassword(newPassword); err != nil {
		return err
	}
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(oldPassword)) != nil {
		return apperr.InvalidField("old_password", "current password is incorrect")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	if err != nil {
		return err
	}
	user.PasswordHash = string(hash)
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return err
	}
	if err := s.store.DeleteUserSessions(ctx, userID); err != nil {
		return err
	}
	_ = s.store.DeleteUserAPITokens(ctx, userID)
	if s.passwordChanged != nil {
		s.passwordChanged()
	}
	return nil
}

// SessionID returns a short, non-reversible identifier for a raw session token.
func SessionID(token string) string {
	return SessionIDFromHash(hashToken(token))
}

// SessionIDFromHash shortens a stored token hash for display.
func SessionIDFromHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// Sessions lists active sessions for a user.
func (s *Service) Sessions(ctx context.Context, userID uint) ([]storage.Session, error) {
	return s.store.ListSessions(ctx, userID)
}

// RevokeSession removes a session by its short identifier. The current session
// is rejected so callers use logout instead.
func (s *Service) RevokeSession(ctx context.Context, userID uint, id, currentToken string) error {
	if len(id) < 8 || len(id) > 64 || !isHex(id) {
		return apperr.Invalidf("invalid session id")
	}
	if id == SessionID(currentToken) {
		return apperr.InvalidField("id", "cannot revoke the current session; use logout")
	}
	rows, err := s.store.DeleteSessionByPrefix(ctx, userID, id)
	if err != nil {
		return err
	}
	if rows == 0 {
		return apperr.NotFoundf("session not found")
	}
	return nil
}

// RevokeOtherSessions keeps the current session and removes all others.
func (s *Service) RevokeOtherSessions(ctx context.Context, userID uint, currentToken string) (int64, error) {
	if currentToken == "" {
		return 0, apperr.New(apperr.Unauthorized, "missing session")
	}
	return s.store.DeleteOtherSessions(ctx, userID, hashToken(currentToken))
}

func isHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// CleanupSessions removes expired rows.
func (s *Service) CleanupSessions(ctx context.Context) {
	n, err := s.store.DeleteExpiredSessions(ctx)
	if err != nil {
		s.log.Warn("cleanup sessions", "error", err)
		return
	}
	if n > 0 {
		s.log.Debug("removed expired sessions", "count", n)
	}
}

func (s *Service) blocked(key string) bool {
	s.settingsMu.RLock()
	enabled := s.lockoutEnabled
	s.settingsMu.RUnlock()
	if !enabled {
		return false
	}
	maxFailures, _ := s.loginPolicy()
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.attempts[key]
	if a == nil {
		return false
	}
	if time.Now().After(a.until) {
		delete(s.attempts, key)
		return false
	}
	return a.count >= maxFailures
}

func (s *Service) fail(key string) {
	s.settingsMu.RLock()
	enabled := s.lockoutEnabled
	s.settingsMu.RUnlock()
	if !enabled {
		return
	}
	_, lockWindow := s.loginPolicy()
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.attempts[key]
	if a == nil || time.Now().After(a.until) {
		a = &attempt{until: time.Now().Add(lockWindow)}
		s.attempts[key] = a
	}
	a.count++
	a.until = time.Now().Add(lockWindow)
}

func (s *Service) reset(key string) {
	s.mu.Lock()
	delete(s.attempts, key)
	s.mu.Unlock()
}

func reauthKey(userID uint, ip string) string {
	return fmt.Sprintf("reauth:%d|%s", userID, ip)
}

// ReauthBlocked reports whether sensitive-operation password checks are
// temporarily locked for this user/IP pair.
func (s *Service) ReauthBlocked(userID uint, ip string) bool {
	s.settingsMu.RLock()
	maxFailures := s.maxFailures
	s.settingsMu.RUnlock()
	key := reauthKey(userID, ip)
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.reauth[key]
	if a == nil {
		return false
	}
	if time.Now().After(a.until) {
		delete(s.reauth, key)
		return false
	}
	return a.count >= maxFailures
}

// ReauthFailed records a failed password confirmation for a sensitive action.
func (s *Service) ReauthFailed(userID uint, ip string) {
	s.settingsMu.RLock()
	lockWindow := s.lockWindow
	s.settingsMu.RUnlock()
	key := reauthKey(userID, ip)
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.reauth[key]
	if a == nil || time.Now().After(a.until) {
		a = &attempt{until: time.Now().Add(lockWindow)}
		s.reauth[key] = a
	}
	a.count++
	a.until = time.Now().Add(lockWindow)
}

// ReauthSucceeded clears failed password confirmation counters.
func (s *Service) ReauthSucceeded(userID uint, ip string) {
	s.mu.Lock()
	delete(s.reauth, reauthKey(userID, ip))
	s.mu.Unlock()
}

// CleanupAttempts removes expired login/reauth limiter entries so long-running
// processes do not retain keys indefinitely.
func (s *Service) CleanupAttempts() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, a := range s.attempts {
		if a == nil || now.After(a.until) {
			delete(s.attempts, key)
		}
	}
	for key, a := range s.reauth {
		if a == nil || now.After(a.until) {
			delete(s.reauth, key)
		}
	}
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// InitialPasswordFile writes a generated password next to the database.
func InitialPasswordFile(dataDir, password string) (string, error) {
	path := filepath.Join(dataDir, "initial-admin-password.txt")
	if err := os.WriteFile(path, []byte("admin / "+password+"\n"), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// CreateAPIToken creates a scoped bearer token. The plaintext is returned only
// once; only its SHA-256 hash is persisted.
func (s *Service) CreateAPIToken(ctx context.Context, userID uint, name string, readOnly bool, expiresAt *time.Time) (string, *storage.APIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 {
		return "", nil, apperr.InvalidField("name", "token name is required and must be at most 128 characters")
	}
	scopes := []string{"read"}
	if !readOnly {
		scopes = append(scopes, "write")
	}
	raw, err := secure.RandomToken(32)
	if err != nil {
		return "", nil, err
	}
	raw = "pg_" + raw
	row := &storage.APIToken{
		UserID: &userID, Name: name, TokenHash: hashToken(raw), Scopes: storage.StringList(scopes),
	}
	if expiresAt == nil {
		// Tokens should not be immortal by default; automation can still set an
		// explicit later expiry when it really needs a long-lived credential.
		def := time.Now().Add(90 * 24 * time.Hour)
		expiresAt = &def
	}
	utc := expiresAt.UTC()
	if !utc.After(time.Now()) {
		return "", nil, apperr.InvalidField("expires_at", "expiry must be in the future")
	}
	row.ExpiresAt = &utc
	if err := s.store.CreateAPIToken(ctx, row); err != nil {
		return "", nil, err
	}
	return raw, row, nil
}

// AuthenticateAPIToken validates a bearer token and throttles last-used writes.
func (s *Service) AuthenticateAPIToken(ctx context.Context, raw string) (*storage.APIToken, error) {
	if !strings.HasPrefix(raw, "pg_") {
		return nil, apperr.New(apperr.Unauthorized, "invalid API token")
	}
	row, err := s.store.GetAPITokenByHash(ctx, hashToken(raw))
	if err != nil {
		return nil, apperr.New(apperr.Unauthorized, "invalid API token")
	}
	now := time.Now()
	if row.ExpiresAt != nil && !row.ExpiresAt.After(now) {
		return nil, apperr.New(apperr.Unauthorized, "API token expired")
	}
	if row.UserID != nil {
		owner, err := s.store.GetUser(ctx, *row.UserID)
		if err != nil || owner == nil || !owner.Enabled {
			return nil, apperr.New(apperr.Unauthorized, "API token owner is disabled")
		}
	}
	if row.LastUsedAt == nil || now.Sub(*row.LastUsedAt) > time.Minute {
		if err := s.store.TouchAPIToken(ctx, row.ID, now); err == nil {
			row.LastUsedAt = &now
		}
	}
	return row, nil
}

// ListAPITokens returns token metadata without hashes.
func (s *Service) ListAPITokens(ctx context.Context) ([]storage.APIToken, error) {
	return s.store.ListAPITokens(ctx)
}

// DeleteAPIToken revokes a token.
func (s *Service) DeleteAPIToken(ctx context.Context, id uint) error {
	if err := s.store.DeleteAPIToken(ctx, id); err != nil {
		if err == storage.ErrNotFound {
			return apperr.NotFoundf("API token %d not found", id)
		}
		return err
	}
	return nil
}

// HasScope reports whether a token carries scope.
func HasScope(t *storage.APIToken, scope string) bool {
	if t == nil {
		return false
	}
	for _, s := range t.Scopes {
		if s == scope || s == "*" {
			return true
		}
	}
	return false
}
