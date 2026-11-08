package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/auth"
)

type sessionView struct {
	ID            string     `json:"id"`
	ClientIP      string     `json:"client_ip"`
	UserAgent     string     `json:"user_agent"`
	CreatedAt     time.Time  `json:"created_at"`
	LastSeenAt    *time.Time `json:"last_seen_at,omitempty"`
	IdleExpiresAt time.Time  `json:"idle_expires_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	Current       bool       `json:"current"`
}

// ListSessions returns the current administrator's active sessions.
func (s *Server) ListSessions(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		s.fail(w, r, apperr.New(apperr.Unauthorized, "not authenticated"))
		return
	}
	rows, err := s.Auth.Sessions(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	current := auth.SessionID(sessionToken(r))
	items := make([]sessionView, 0, len(rows))
	for _, row := range rows {
		lastSeen := row.CreatedAt
		if row.LastSeenAt != nil {
			lastSeen = *row.LastSeenAt
		}
		items = append(items, sessionView{
			ID: auth.SessionIDFromHash(row.TokenHash), ClientIP: row.ClientIP, UserAgent: row.UserAgent,
			CreatedAt: row.CreatedAt, LastSeenAt: row.LastSeenAt, IdleExpiresAt: lastSeen.Add(s.Auth.SessionTTL()),
			ExpiresAt: row.ExpiresAt, Current: auth.SessionIDFromHash(row.TokenHash) == current,
		})
	}
	response.OK(w, map[string]any{"items": items})
}

// RevokeSession revokes one non-current session.
func (s *Server) RevokeSession(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		s.fail(w, r, apperr.New(apperr.Unauthorized, "not authenticated"))
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.Auth.RevokeSession(r.Context(), u.ID, id, sessionToken(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "revoke_session", "session", id, "success", nil)
	w.WriteHeader(http.StatusNoContent)
}

// RevokeOtherSessions revokes all sessions except the current one.
func (s *Server) RevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		s.fail(w, r, apperr.New(apperr.Unauthorized, "not authenticated"))
		return
	}
	n, err := s.Auth.RevokeOtherSessions(r.Context(), u.ID, sessionToken(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "revoke_other_sessions", "session", nil, "success", map[string]any{"count": n})
	response.OK(w, map[string]any{"revoked": n})
}
