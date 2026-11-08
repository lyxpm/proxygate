package handler

import (
	"net/http"

	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/apperr"
)

// ListUsers returns local accounts for the user management view.
func (s *Server) ListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Auth.ListUsers(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items := make([]userView, 0, len(rows))
	for i := range rows {
		items = append(items, toUserView(&rows[i]))
	}
	response.OK(w, map[string]any{"items": items})
}

// CreateUser creates a local administrator or viewer.
func (s *Server) CreateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	user, err := s.Auth.CreateUser(r.Context(), in.Username, in.Password, in.Role)
	if err != nil {
		s.audit(r, "create_user", "user", nil, "failure", map[string]any{"username": in.Username})
		s.fail(w, r, err)
		return
	}
	s.audit(r, "create_user", "user", user.ID, "success", map[string]any{"username": user.Username, "role": user.Role})
	response.OK(w, toUserView(user))
}

// UpdateUser changes role, enabled state or resets a password.
func (s *Server) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Role     string `json:"role"`
		Enabled  bool   `json:"enabled"`
		Password string `json:"password"`
	}
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if u := userFrom(r); u != nil && u.ID == id && (in.Role != "admin" || !in.Enabled) {
		s.fail(w, r, apperr.Invalidf("不能停用或降级当前登录账号"))
		return
	}
	user, err := s.Auth.UpdateUser(r.Context(), id, in.Role, in.Enabled, in.Password)
	if err != nil {
		s.audit(r, "update_user", "user", id, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "update_user", "user", id, "success", map[string]any{"role": user.Role, "enabled": user.Enabled})
	response.OK(w, toUserView(user))
}

// DeleteUser removes an account.
func (s *Server) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if u := userFrom(r); u != nil && u.ID == id {
		s.fail(w, r, apperr.Invalidf("不能删除当前登录账号"))
		return
	}
	if err := s.Auth.DeleteUser(r.Context(), id); err != nil {
		s.audit(r, "delete_user", "user", id, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "delete_user", "user", id, "success", nil)
	w.WriteHeader(http.StatusNoContent)
}
