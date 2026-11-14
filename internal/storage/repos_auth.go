package storage

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// CreateUser inserts an administrator.
func (s *Store) CreateUser(ctx context.Context, u *User) error {
	return s.DB.WithContext(ctx).Create(u).Error
}

// GetUserByUsername looks up an administrator by login name.
func (s *Store) GetUserByUsername(ctx context.Context, name string) (*User, error) {
	var u User
	if err := s.DB.WithContext(ctx).Where("username = ?", name).First(&u).Error; err != nil {
		return nil, wrapErr(err)
	}
	return &u, nil
}

// GetUser loads an administrator by id.
func (s *Store) GetUser(ctx context.Context, id uint) (*User, error) {
	var u User
	if err := s.DB.WithContext(ctx).First(&u, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return &u, nil
}

// UpdateUser saves administrator fields.
func (s *Store) UpdateUser(ctx context.Context, u *User) error {
	return s.DB.WithContext(ctx).Save(u).Error
}

// ClaimTOTPStep atomically records a TOTP time step, rejecting replay.
func (s *Store) ClaimTOTPStep(ctx context.Context, userID uint, step int64) (bool, error) {
	res := s.DB.WithContext(ctx).Model(&User{}).
		Where("id = ? AND last_totp_step < ?", userID, step).
		Update("last_totp_step", step)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// CountUsers returns the number of administrators.
func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	var n int64
	err := s.DB.WithContext(ctx).Model(&User{}).Count(&n).Error
	return n, err
}

// ListUsers returns all accounts newest first.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	var out []User
	err := s.DB.WithContext(ctx).Order("id").Find(&out).Error
	return out, err
}

// CountAdmins returns enabled administrators.
func (s *Store) CountAdmins(ctx context.Context) (int64, error) {
	var n int64
	err := s.DB.WithContext(ctx).Model(&User{}).Where("role = ? AND enabled = ?", "admin", true).Count(&n).Error
	return n, err
}

// DeleteUser removes an account and all of its sessions and API tokens.
func (s *Store) DeleteUser(ctx context.Context, id uint) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", id).Delete(&Session{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&APIToken{}).Error; err != nil {
			return err
		}
		return tx.Delete(&User{}, id).Error
	})
}

// DeleteUserAPITokens revokes every API token owned by a user.
func (s *Store) DeleteUserAPITokens(ctx context.Context, userID uint) error {
	return s.DB.WithContext(ctx).Where("user_id = ?", userID).Delete(&APIToken{}).Error
}

// CreateSession stores a hashed session token.
func (s *Store) CreateSession(ctx context.Context, sess *Session) error {
	return s.DB.WithContext(ctx).Create(sess).Error
}

// GetSession returns a session when it exists and is not expired.
func (s *Store) GetSession(ctx context.Context, tokenHash string) (*Session, error) {
	var sess Session
	err := s.DB.WithContext(ctx).Where("token_hash = ? AND expires_at > ?", tokenHash, time.Now().UTC()).First(&sess).Error
	if err != nil {
		return nil, wrapErr(err)
	}
	return &sess, nil
}

// TouchSession updates the last activity time for idle-timeout enforcement.
func (s *Store) TouchSession(ctx context.Context, tokenHash string, at time.Time) error {
	return s.DB.WithContext(ctx).Model(&Session{}).Where("token_hash = ?", tokenHash).Update("last_seen_at", at).Error
}

// DeleteSession removes one session.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	return s.DB.WithContext(ctx).Delete(&Session{}, "token_hash = ?", tokenHash).Error
}

// DeleteExpiredSessions prunes expired rows.
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res := s.DB.WithContext(ctx).Where("expires_at <= ?", time.Now().UTC()).Delete(&Session{})
	return res.RowsAffected, res.Error
}

// DeleteUserSessions removes every session of a user.
func (s *Store) DeleteUserSessions(ctx context.Context, userID uint) error {
	return s.DB.WithContext(ctx).Where("user_id = ?", userID).Delete(&Session{}).Error
}

// ListSessions returns unexpired sessions for a user, newest first.
func (s *Store) ListSessions(ctx context.Context, userID uint) ([]Session, error) {
	var out []Session
	err := s.DB.WithContext(ctx).
		Where("user_id = ? AND expires_at > ?", userID, time.Now().UTC()).
		Order("created_at DESC").Find(&out).Error
	return out, err
}

// DeleteSessionByPrefix revokes one session identified by a token-hash prefix.
func (s *Store) DeleteSessionByPrefix(ctx context.Context, userID uint, prefix string) (int64, error) {
	res := s.DB.WithContext(ctx).
		Where("user_id = ? AND token_hash LIKE ?", userID, prefix+"%").
		Delete(&Session{})
	return res.RowsAffected, res.Error
}

// DeleteOtherSessions revokes every session except the current token hash.
func (s *Store) DeleteOtherSessions(ctx context.Context, userID uint, currentHash string) (int64, error) {
	res := s.DB.WithContext(ctx).
		Where("user_id = ? AND token_hash <> ?", userID, currentHash).
		Delete(&Session{})
	return res.RowsAffected, res.Error
}

// ListDNSProviders returns all DNS provider records.
func (s *Store) ListDNSProviders(ctx context.Context) ([]DNSProvider, error) {
	var out []DNSProvider
	err := s.DB.WithContext(ctx).Order("id").Find(&out).Error
	return out, err
}

// GetDNSProvider loads one provider.
func (s *Store) GetDNSProvider(ctx context.Context, id uint) (*DNSProvider, error) {
	var p DNSProvider
	if err := s.DB.WithContext(ctx).First(&p, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return &p, nil
}

// CreateDNSProvider inserts a provider.
func (s *Store) CreateDNSProvider(ctx context.Context, p *DNSProvider) error {
	return s.DB.WithContext(ctx).Create(p).Error
}

// UpdateDNSProvider saves a provider.
func (s *Store) UpdateDNSProvider(ctx context.Context, p *DNSProvider) error {
	return s.DB.WithContext(ctx).Save(p).Error
}

// DeleteDNSProvider removes a provider.
func (s *Store) DeleteDNSProvider(ctx context.Context, id uint) error {
	res := s.DB.WithContext(ctx).Delete(&DNSProvider{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListACMEAccounts returns all ACME accounts.
func (s *Store) ListACMEAccounts(ctx context.Context) ([]ACMEAccount, error) {
	var out []ACMEAccount
	err := s.DB.WithContext(ctx).Order("id").Find(&out).Error
	return out, err
}

// GetACMEAccount loads one account.
func (s *Store) GetACMEAccount(ctx context.Context, id uint) (*ACMEAccount, error) {
	var a ACMEAccount
	if err := s.DB.WithContext(ctx).First(&a, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return &a, nil
}

// FindACMEAccount returns an account for email+CA or nil.
func (s *Store) FindACMEAccount(ctx context.Context, email, caURL string) (*ACMEAccount, error) {
	var a ACMEAccount
	err := s.DB.WithContext(ctx).Where("email = ? AND ca_url = ?", email, caURL).First(&a).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, wrapErr(err)
	}
	return &a, nil
}

// CreateACMEAccount inserts an account.
func (s *Store) CreateACMEAccount(ctx context.Context, a *ACMEAccount) error {
	return s.DB.WithContext(ctx).Create(a).Error
}

// UpdateACMEAccount saves an account.
func (s *Store) UpdateACMEAccount(ctx context.Context, a *ACMEAccount) error {
	return s.DB.WithContext(ctx).Save(a).Error
}

// DeleteACMEAccount removes an account.
func (s *Store) DeleteACMEAccount(ctx context.Context, id uint) error {
	res := s.DB.WithContext(ctx).Delete(&ACMEAccount{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
