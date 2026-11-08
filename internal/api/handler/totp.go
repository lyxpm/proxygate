package handler

import (
	"encoding/base64"
	"net/http"

	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/apperr"
	qrcode "github.com/skip2/go-qrcode"
)

// TOTPStatus returns whether two-factor authentication is enabled.
func (s *Server) TOTPStatus(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		s.fail(w, r, apperr.New(apperr.Unauthorized, "not authenticated"))
		return
	}
	enabled, err := s.Auth.TOTPEnabled(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, map[string]any{"enabled": enabled})
}

// SetupTOTP creates a pending TOTP seed and returns it once.
func (s *Server) SetupTOTP(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		s.fail(w, r, apperr.New(apperr.Unauthorized, "not authenticated"))
		return
	}
	secret, otpauth, err := s.Auth.SetupTOTP(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	qr := ""
	if png, qerr := qrcode.Encode(otpauth, qrcode.Medium, 220); qerr == nil {
		qr = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	response.OK(w, map[string]any{"secret": secret, "otpauth_url": otpauth, "qr_data_url": qr})
}

// EnableTOTP verifies a code and activates two-factor authentication.
func (s *Server) EnableTOTP(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		s.fail(w, r, apperr.New(apperr.Unauthorized, "not authenticated"))
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Auth.EnableTOTP(r.Context(), u.ID, in.Code); err != nil {
		s.audit(r, "enable_totp", "user", u.ID, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "enable_totp", "user", u.ID, "success", nil)
	_, _ = s.Auth.RevokeOtherSessions(r.Context(), u.ID, sessionToken(r))
	response.OK(w, map[string]any{"enabled": true})
}

// DisableTOTP removes two-factor authentication after password confirmation.
func (s *Server) DisableTOTP(w http.ResponseWriter, r *http.Request) {
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
		s.audit(r, "disable_totp", "user", u.ID, "failure", nil)
		return
	}
	if err := s.Auth.DisableTOTP(r.Context(), u.ID, in.Password); err != nil {
		s.audit(r, "disable_totp", "user", u.ID, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "disable_totp", "user", u.ID, "success", nil)
	response.OK(w, map[string]any{"enabled": false})
}
