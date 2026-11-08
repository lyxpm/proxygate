package auth

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/proxygate/proxygate/internal/secure"
	"github.com/proxygate/proxygate/internal/storage"
)

func newAuth(t *testing.T) (*Service, *storage.Store) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "auth.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := New(store, time.Hour, nil)
	if _, err := svc.EnsureAdmin(context.Background(), "admin12345"); err != nil {
		t.Fatal(err)
	}
	return svc, store
}

func TestLoginSessionAndPasswordChange(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	user, token, err := svc.Login(ctx, "admin", "admin12345", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "admin" || token == "" {
		t.Fatalf("unexpected login result: %+v %q", user, token)
	}
	got, err := svc.Authenticate(ctx, token)
	if err != nil || got.ID != user.ID {
		t.Fatalf("authenticate failed: %v", err)
	}
	if err := svc.ChangePassword(ctx, user.ID, "admin12345", "newpassword1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, token); err == nil {
		t.Fatal("session should be invalid after password change")
	}
	if _, _, err := svc.Login(ctx, "admin", "newpassword1", "127.0.0.1", "test"); err != nil {
		t.Fatalf("login with new password failed: %v", err)
	}
}

func TestAPITokens(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	raw, row, err := svc.CreateAPIToken(ctx, "ci", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 20 || raw[:3] != "pg_" {
		t.Fatalf("unexpected token format: %q", raw)
	}
	if !HasScope(row, "read") || HasScope(row, "write") {
		t.Fatalf("unexpected scopes: %v", row.Scopes)
	}
	got, err := svc.AuthenticateAPIToken(ctx, raw)
	if err != nil || got.ID != row.ID {
		t.Fatalf("authenticate token: %v", err)
	}
	if _, err := svc.AuthenticateAPIToken(ctx, "pg_invalid"); err == nil {
		t.Fatal("expected invalid token error")
	}
	if err := svc.DeleteAPIToken(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateAPIToken(ctx, raw); err == nil {
		t.Fatal("deleted token should not authenticate")
	}
}

func TestVerifyPassword(t *testing.T) {
	svc, _ := newAuth(t)
	user, _, err := svc.Login(context.Background(), "admin", "admin12345", "10.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyPassword(context.Background(), user.ID, "admin12345"); err != nil {
		t.Fatalf("correct password rejected: %v", err)
	}
	if err := svc.VerifyPassword(context.Background(), user.ID, "wrong-password"); err == nil {
		t.Fatal("wrong password accepted")
	}
}

func TestSessionManagement(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	user, token1, err := svc.Login(ctx, "admin", "admin12345", "10.0.0.1", "one")
	if err != nil {
		t.Fatal(err)
	}
	_, token2, err := svc.Login(ctx, "admin", "admin12345", "10.0.0.2", "two")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := svc.Sessions(ctx, user.ID)
	if err != nil || len(sessions) < 2 {
		t.Fatalf("expected sessions: %v %d", err, len(sessions))
	}
	if err := svc.RevokeSession(ctx, user.ID, SessionID(token2), token1); err != nil {
		t.Fatalf("revoke other session: %v", err)
	}
	if _, err := svc.Authenticate(ctx, token2); err == nil {
		t.Fatal("revoked session still valid")
	}
	if err := svc.RevokeSession(ctx, user.ID, SessionID(token1), token1); err == nil {
		t.Fatal("current session must not be revocable via this path")
	}
	_, token3, err := svc.Login(ctx, "admin", "admin12345", "10.0.0.3", "three")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RevokeOtherSessions(ctx, user.ID, token3); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, token1); err == nil {
		t.Fatal("other sessions should be revoked")
	}
	if _, err := svc.Authenticate(ctx, token3); err != nil {
		t.Fatalf("current session should survive: %v", err)
	}
}

func TestTOTPCode(t *testing.T) {
	// RFC 6238 SHA-1 test vector, six-digit suffix.
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	code, err := totpCode(secret, time.Unix(59, 0))
	if err != nil {
		t.Fatal(err)
	}
	if code != "287082" {
		t.Fatalf("code = %s, want 287082", code)
	}
	if !validateTOTP(secret, code, time.Unix(59, 0)) {
		t.Fatal("valid code rejected")
	}
	if validateTOTP(secret, "000000", time.Unix(59, 0)) {
		t.Fatal("invalid code accepted")
	}
}

func TestTOTPEnableAndLogin(t *testing.T) {
	svc, store := newAuth(t)
	box, err := secure.NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	svc.SetBox(box)
	ctx := context.Background()
	user, err := store.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	secret, url, err := svc.SetupTOTP(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" || url == "" {
		t.Fatal("empty setup result")
	}
	code, err := totpCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.EnableTOTP(ctx, user.ID, code); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Login(ctx, "admin", "admin12345", "10.1.1.1", "test"); err == nil {
		t.Fatal("login without TOTP code should fail")
	}
	code, _ = totpCode(secret, time.Now())
	if _, _, err := svc.Login(ctx, "admin", "admin12345", "10.1.1.1", "test", code); err != nil {
		t.Fatalf("login with TOTP failed: %v", err)
	}
	if err := svc.DisableTOTP(ctx, user.ID, "admin12345"); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := svc.TOTPEnabled(ctx, user.ID); enabled {
		t.Fatal("TOTP should be disabled")
	}
}

func TestUsersAndRoles(t *testing.T) {
	svc, store := newAuth(t)
	ctx := context.Background()
	viewer, err := svc.CreateUser(ctx, "viewer1", "viewerpass1", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	if viewer.Role != "viewer" {
		t.Fatalf("role = %s", viewer.Role)
	}
	u, _, err := svc.Login(ctx, "viewer1", "viewerpass1", "10.2.2.2", "test")
	if err != nil {
		t.Fatal(err)
	}
	if RoleOf(u) != "viewer" {
		t.Fatalf("login role = %s", RoleOf(u))
	}
	admin, err := store.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteUser(ctx, admin.ID); err == nil {
		t.Fatal("last administrator must not be deleted")
	}
	if _, err := svc.UpdateUser(ctx, admin.ID, "viewer", true, ""); err == nil {
		t.Fatal("last administrator must not be demoted")
	}
	if _, err := svc.UpdateUser(ctx, viewer.ID, "admin", true, "newpass123"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteUser(ctx, admin.ID); err != nil {
		t.Fatalf("delete admin with another admin present: %v", err)
	}
}

func TestPasswordChangedHook(t *testing.T) {
	svc, store := newAuth(t)
	ctx := context.Background()
	user, err := store.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	svc.SetPasswordChangedHook(func() { called = true })
	if err := svc.ChangePassword(ctx, user.ID, "admin12345", "newpass12345"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("password change hook was not called")
	}
}

func TestSensitiveReauthRateLimit(t *testing.T) {
	svc, store := newAuth(t)
	ctx := context.Background()
	user, err := store.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < svc.maxFailures; i++ {
		svc.ReauthFailed(user.ID, "10.0.0.9")
	}
	if !svc.ReauthBlocked(user.ID, "10.0.0.9") {
		t.Fatal("reauthentication should be blocked after repeated failures")
	}
	svc.ReauthSucceeded(user.ID, "10.0.0.9")
	if svc.ReauthBlocked(user.ID, "10.0.0.9") {
		t.Fatal("successful reauthentication should clear the counter")
	}
}

func TestTOTPReplayRejected(t *testing.T) {
	svc, store := newAuth(t)
	box, _ := secure.NewBox(bytes.Repeat([]byte{9}, 32))
	svc.SetBox(box)
	ctx := context.Background()
	user, err := store.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := svc.SetupTOTP(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totpCode(secret, time.Now())
	if err := svc.EnableTOTP(ctx, user.ID, code); err != nil {
		t.Fatal(err)
	}
	code, _ = totpCode(secret, time.Now())
	if _, _, err := svc.Login(ctx, "admin", "admin12345", "10.9.9.9", "test", code); err != nil {
		t.Fatalf("first TOTP login failed: %v", err)
	}
	if _, _, err := svc.Login(ctx, "admin", "admin12345", "10.9.9.9", "test", code); err == nil {
		t.Fatal("replayed TOTP code should be rejected")
	}
}

func TestPasswordPolicy(t *testing.T) {
	svc, store := newAuth(t)
	ctx := context.Background()
	for key, value := range map[string]string{
		"password_policy_enabled": "1",
		"password_min_length":     "12",
		"password_require_upper":  "1",
		"password_require_lower":  "1",
		"password_require_digit":  "1",
		"password_require_symbol": "1",
	} {
		if err := store.SetSetting(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.ReloadSecuritySettings(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateUser(ctx, "weakuser", "short1", "viewer"); err == nil {
		t.Fatal("weak password accepted")
	}
	if _, err := svc.CreateUser(ctx, "stronguser", "StrongPass1!", "viewer"); err != nil {
		t.Fatalf("strong password rejected: %v", err)
	}
}

func TestSecuritySwitchSeparation(t *testing.T) {
	svc, store := newAuth(t)
	ctx := context.Background()
	if err := store.SetSetting(ctx, "login_protection_enabled", "0"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(ctx, "session_idle_minutes", "30"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReloadSecuritySettings(ctx); err != nil {
		t.Fatal(err)
	}
	if got := svc.SessionTTL(); got != 30*24*time.Hour {
		t.Fatalf("disabled login protection TTL = %v", got)
	}
	for i := 0; i < maxFailures+2; i++ {
		if _, _, err := svc.Login(ctx, "admin", "wrong", "10.8.8.8", "test"); err == nil {
			t.Fatal("expected failure")
		}
	}
	if _, _, err := svc.Login(ctx, "admin", "admin12345", "10.8.8.8", "test"); err != nil {
		t.Fatalf("lockout should be disabled: %v", err)
	}
}

func TestIdleTimeout(t *testing.T) {
	svc, store := newAuth(t)
	ctx := context.Background()
	if err := store.SetSetting(ctx, "login_protection_enabled", "1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(ctx, "session_idle_minutes", "5"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReloadSecuritySettings(ctx); err != nil {
		t.Fatal(err)
	}
	_, token, err := svc.Login(ctx, "admin", "admin12345", "10.7.7.7", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, token); err != nil {
		t.Fatalf("fresh session rejected: %v", err)
	}
	old := time.Now().Add(-10 * time.Minute)
	if err := store.TouchSession(ctx, hashToken(token), old); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, token); err == nil {
		t.Fatal("idle session should be rejected")
	}
}

func TestLoginRateLimit(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	for i := 0; i < maxFailures; i++ {
		if _, _, err := svc.Login(ctx, "admin", "wrong", "10.0.0.1", "test"); err == nil {
			t.Fatal("expected login failure")
		}
	}
	if _, _, err := svc.Login(ctx, "admin", "admin12345", "10.0.0.1", "test"); err == nil {
		t.Fatal("expected rate limit to block even correct password")
	}
	// A different client IP is unaffected.
	if _, _, err := svc.Login(ctx, "admin", "admin12345", "10.0.0.2", "test"); err != nil {
		t.Fatalf("other IP blocked: %v", err)
	}
}
