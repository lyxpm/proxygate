package handler

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/api/middleware"
	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/storage"
)

type userView struct {
	ID          uint       `json:"id"`
	Username    string     `json:"username"`
	Role        string     `json:"role"`
	Enabled     bool       `json:"enabled"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

func toUserView(u *storage.User) userView {
	role := u.Role
	if role == "" {
		role = "admin"
	}
	return userView{ID: u.ID, Username: u.Username, Role: role, Enabled: u.Enabled, LastLoginAt: u.LastLoginAt}
}

// Login authenticates and sets the session cookie.
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	user, token, err := s.Auth.Login(r.Context(), in.Username, in.Password, clientIP(r), r.UserAgent(), in.TOTPCode)
	if err != nil {
		s.audit(r, "login", "user", nil, "failure", map[string]any{"username": strings.TrimSpace(in.Username)})
		s.fail(w, r, err)
		return
	}
	s.setSessionCookie(w, r, token, s.Auth.SessionCookieTTL())
	_ = os.Remove(filepath.Join(s.Config.DataDir, "initial-admin-password.txt"))
	s.auditForUser(r, user.ID, "login", "user", user.ID, "success", nil)
	response.OK(w, map[string]any{"user": toUserView(user), "expires_in": int(s.Auth.SessionTTL().Seconds())})
}

// Logout deletes the current session.
func (s *Server) Logout(w http.ResponseWriter, r *http.Request) {
	token := sessionToken(r)
	if err := s.Auth.Logout(r.Context(), token); err != nil {
		s.Logger.Warn("logout", "error", err)
	}
	http.SetCookie(w, &http.Cookie{Name: middleware.SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	clearCSRFCookie(w, r)
	s.audit(r, "logout", "user", nil, "success", nil)
	response.OK(w, nil)
}

// Me returns the current user.
func (s *Server) Me(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		s.fail(w, r, apperr.New(apperr.Unauthorized, "not authenticated"))
		return
	}
	response.OK(w, map[string]any{"user": toUserView(u)})
}

// ChangePassword updates the password and invalidates all sessions.
func (s *Server) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	u := userFrom(r)
	if u == nil {
		s.fail(w, r, apperr.New(apperr.Unauthorized, "not authenticated"))
		return
	}
	if !s.verifyReauth(w, r, in.OldPassword) {
		s.audit(r, "change_password", "user", u.ID, "failure", nil)
		return
	}
	if err := s.Auth.ChangePassword(r.Context(), u.ID, in.OldPassword, in.NewPassword); err != nil {
		s.audit(r, "change_password", "user", u.ID, "failure", nil)
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: middleware.SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	clearCSRFCookie(w, r)
	s.audit(r, "change_password", "user", u.ID, "success", nil)
	response.OK(w, map[string]any{"message": "password changed; please sign in again"})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name: middleware.SessionCookie, Value: token, Path: "/",
		MaxAge: int(ttl.Seconds()), HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	setCSRFCookie(w, secure, int(ttl.Seconds()))
}

func setCSRFCookie(w http.ResponseWriter, secure bool, maxAge int) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: middleware.CSRFCookie, Value: hex.EncodeToString(raw[:]), Path: "/",
		MaxAge: maxAge, HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func clearCSRFCookie(w http.ResponseWriter, r *http.Request) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name: middleware.CSRFCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func sessionToken(r *http.Request) string {
	if c, err := r.Cookie(middleware.SessionCookie); err == nil {
		return c.Value
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}
