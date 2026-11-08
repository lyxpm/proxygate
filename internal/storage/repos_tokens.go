package storage

import (
	"context"
	"time"
)

// CreateAPIToken inserts a token hash.
func (s *Store) CreateAPIToken(ctx context.Context, t *APIToken) error {
	return s.DB.WithContext(ctx).Create(t).Error
}

// ListAPITokens returns tokens newest first.
func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	var out []APIToken
	err := s.DB.WithContext(ctx).Order("id DESC").Find(&out).Error
	return out, err
}

// GetAPITokenByHash looks up a token by its SHA-256 hash.
func (s *Store) GetAPITokenByHash(ctx context.Context, hash string) (*APIToken, error) {
	var t APIToken
	if err := s.DB.WithContext(ctx).Where("token_hash = ?", hash).First(&t).Error; err != nil {
		return nil, wrapErr(err)
	}
	return &t, nil
}

// DeleteAPIToken removes a token.
func (s *Store) DeleteAPIToken(ctx context.Context, id uint) error {
	res := s.DB.WithContext(ctx).Delete(&APIToken{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchAPIToken updates last_used_at.
func (s *Store) TouchAPIToken(ctx context.Context, id uint, at time.Time) error {
	return s.DB.WithContext(ctx).Model(&APIToken{}).Where("id = ?", id).Update("last_used_at", at).Error
}
