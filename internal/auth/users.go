package auth

import (
	"context"
	"regexp"
	"strings"

	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,64}$`)

// RoleOf normalizes legacy rows where role was added after the account.
func RoleOf(u *storage.User) string {
	if u == nil {
		return ""
	}
	if u.Role == "" {
		return "admin"
	}
	return u.Role
}

// ListUsers returns all accounts.
func (s *Service) ListUsers(ctx context.Context) ([]storage.User, error) {
	return s.store.ListUsers(ctx)
}

// CreateUser creates a local account with a role.
func (s *Service) CreateUser(ctx context.Context, username, password, role string) (*storage.User, error) {
	username = strings.TrimSpace(username)
	if !usernamePattern.MatchString(username) {
		return nil, apperr.InvalidField("username", "用户名需为 3-64 位字母、数字、点、下划线或短横线")
	}
	if len(password) < 8 {
		return nil, apperr.InvalidField("password", "密码至少 8 位")
	}
	if err := s.ValidatePassword(password); err != nil {
		return nil, err
	}
	role = normalizeRole(role)
	if role == "" {
		return nil, apperr.InvalidField("role", "角色必须是 admin 或 viewer")
	}
	if _, err := s.store.GetUserByUsername(ctx, username); err == nil {
		return nil, apperr.Conflictf("用户名已存在")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, err
	}
	user := &storage.User{Username: username, PasswordHash: string(hash), Enabled: true, Role: role}
	if err := s.store.CreateUser(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

// UpdateUser changes role, enabled state and optionally resets the password.
func (s *Service) UpdateUser(ctx context.Context, id uint, role string, enabled bool, password string) (*storage.User, error) {
	user, err := s.store.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	role = normalizeRole(role)
	if role == "" {
		return nil, apperr.InvalidField("role", "角色必须是 admin 或 viewer")
	}
	if password != "" {
		if err := s.ValidatePassword(password); err != nil {
			return nil, err
		}
	}
	losingAdmin := RoleOf(user) == "admin" && (role != "admin" || !enabled)
	if losingAdmin {
		admins, err := s.store.CountAdmins(ctx)
		if err != nil {
			return nil, err
		}
		if admins <= 1 {
			return nil, apperr.Invalidf("不能停用或降级最后一个管理员")
		}
	}
	user.Role = role
	user.Enabled = enabled
	if password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
		if err != nil {
			return nil, err
		}
		user.PasswordHash = string(hash)
	}
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return nil, err
	}
	if !enabled || password != "" {
		_ = s.store.DeleteUserSessions(ctx, id)
	}
	if !enabled || password != "" {
		_ = s.store.DeleteUserAPITokens(ctx, id)
	}
	if password != "" && s.passwordChanged != nil {
		s.passwordChanged()
	}
	return user, nil
}

// DeleteUser removes an account unless it is the last administrator.
func (s *Service) DeleteUser(ctx context.Context, id uint) error {
	user, err := s.store.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if RoleOf(user) == "admin" {
		admins, err := s.store.CountAdmins(ctx)
		if err != nil {
			return err
		}
		if admins <= 1 {
			return apperr.Invalidf("不能删除最后一个管理员")
		}
	}
	return s.store.DeleteUser(ctx, id)
}

func normalizeRole(role string) string {
	switch strings.TrimSpace(role) {
	case "admin":
		return "admin"
	case "viewer":
		return "viewer"
	default:
		return ""
	}
}
