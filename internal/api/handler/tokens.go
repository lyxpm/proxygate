package handler

import (
	"net/http"
	"time"

	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/storage"
)

type tokenView struct {
	ID         uint       `json:"id"`
	UserID     *uint      `json:"user_id,omitempty"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toTokenView(t storage.APIToken) tokenView {
	return tokenView{
		ID: t.ID, UserID: t.UserID, Name: t.Name, Scopes: t.Scopes,
		ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, CreatedAt: t.CreatedAt,
	}
}

// ListAPITokens returns token metadata (never plaintext or hashes).
func (s *Server) ListAPITokens(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Auth.ListAPITokens(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items := make([]tokenView, 0, len(rows))
	for _, row := range rows {
		items = append(items, toTokenView(row))
	}
	response.OK(w, map[string]any{"items": items})
}

// CreateAPIToken issues a scoped token and returns the plaintext once.
func (s *Server) CreateAPIToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string     `json:"name"`
		ReadOnly  bool       `json:"read_only"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if u := userFrom(r); u == nil {
		s.fail(w, r, apperr.New(apperr.Unauthorized, "not authenticated"))
		return
	} else {
		raw, row, err := s.Auth.CreateAPIToken(r.Context(), u.ID, in.Name, in.ReadOnly, in.ExpiresAt)
		if err != nil {
			s.audit(r, "create_api_token", "api_token", nil, "failure", nil)
			s.fail(w, r, err)
			return
		}
		s.audit(r, "create_api_token", "api_token", row.ID, "success", map[string]any{"name": row.Name, "read_only": in.ReadOnly})
		response.Created(w, map[string]any{"token": raw, "item": toTokenView(*row)})
	}
}

// DeleteAPIToken revokes a token.
func (s *Server) DeleteAPIToken(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Auth.DeleteAPIToken(r.Context(), id); err != nil {
		s.audit(r, "delete_api_token", "api_token", id, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "delete_api_token", "api_token", id, "success", nil)
	w.WriteHeader(http.StatusNoContent)
}
