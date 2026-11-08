package certificate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/certificate/provider"
	"github.com/proxygate/proxygate/internal/storage"
)

// DNSProviderInput is the create/update payload for a DNS provider.
type DNSProviderInput struct {
	Name         string               `json:"name"`
	ProviderType string               `json:"provider_type"`
	Credentials  provider.Credentials `json:"credentials"`
	Enabled      *bool                `json:"enabled"`
}

// DNSProviderView omits encrypted credentials.
type DNSProviderView struct {
	ID           uint          `json:"id"`
	Name         string        `json:"name"`
	ProviderType string        `json:"provider_type"`
	Enabled      bool          `json:"enabled"`
	Meta         provider.Meta `json:"meta"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// ListDNSProviders returns providers without secrets.
func (m *Manager) ListDNSProviders(ctx context.Context) ([]DNSProviderView, error) {
	rows, err := m.store.ListDNSProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DNSProviderView, 0, len(rows))
	for _, p := range rows {
		meta, _ := provider.Lookup(p.ProviderType)
		out = append(out, DNSProviderView{
			ID: p.ID, Name: p.Name, ProviderType: p.ProviderType, Enabled: p.Enabled,
			Meta: meta, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		})
	}
	return out, nil
}

// CreateDNSProvider validates, encrypts and stores credentials.
func (m *Manager) CreateDNSProvider(ctx context.Context, in DNSProviderInput) (*DNSProviderView, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperr.InvalidField("name", "name is required")
	}
	meta, ok := provider.Lookup(in.ProviderType)
	if !ok {
		return nil, apperr.InvalidField("provider_type", "unsupported DNS provider")
	}
	if strings.TrimSpace(in.Credentials.Secret) == "" && strings.TrimSpace(in.Credentials.Extra["api_token"]) == "" {
		return nil, apperr.InvalidField("credentials", "credentials are required")
	}
	_ = meta
	p := &storage.DNSProvider{
		Name: name, ProviderType: in.ProviderType, Enabled: boolOr(in.Enabled, true),
		CredentialsEncrypted: "pending",
	}
	if err := m.store.CreateDNSProvider(ctx, p); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, apperr.Conflictf("a DNS provider named %q already exists", name)
		}
		return nil, err
	}
	raw, err := json.Marshal(in.Credentials)
	if err != nil {
		return nil, err
	}
	sealed, err := m.box.Seal(raw, fmt.Sprintf("dns-provider:%d", p.ID))
	if err != nil {
		_ = m.store.DeleteDNSProvider(ctx, p.ID)
		return nil, err
	}
	p.CredentialsEncrypted = sealed
	if err := m.store.UpdateDNSProvider(ctx, p); err != nil {
		_ = m.store.DeleteDNSProvider(ctx, p.ID)
		return nil, err
	}
	return &DNSProviderView{ID: p.ID, Name: p.Name, ProviderType: p.ProviderType, Enabled: p.Enabled, Meta: meta, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}, nil
}

// UpdateDNSProvider changes name/enabled and optionally replaces credentials.
func (m *Manager) UpdateDNSProvider(ctx context.Context, id uint, in DNSProviderInput) (*DNSProviderView, error) {
	p, err := m.store.GetDNSProvider(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, apperr.NotFoundf("DNS provider %d not found", id)
	}
	if err != nil {
		return nil, err
	}
	meta, ok := provider.Lookup(in.ProviderType)
	if !ok {
		return nil, apperr.InvalidField("provider_type", "unsupported DNS provider")
	}
	if name := strings.TrimSpace(in.Name); name != "" {
		p.Name = name
	}
	if p.ProviderType != in.ProviderType && in.Credentials.Secret == "" && in.Credentials.ID == "" && len(in.Credentials.Extra) == 0 {
		return nil, apperr.InvalidField("credentials", "changing the provider type requires new credentials")
	}
	p.ProviderType = in.ProviderType
	if in.Enabled != nil {
		p.Enabled = *in.Enabled
	}
	if in.Credentials.Secret != "" || in.Credentials.ID != "" || len(in.Credentials.Extra) > 0 {
		raw, err := json.Marshal(in.Credentials)
		if err != nil {
			return nil, err
		}
		sealed, err := m.box.Seal(raw, fmt.Sprintf("dns-provider:%d", p.ID))
		if err != nil {
			return nil, err
		}
		p.CredentialsEncrypted = sealed
	}
	if err := m.store.UpdateDNSProvider(ctx, p); err != nil {
		return nil, err
	}
	return &DNSProviderView{ID: p.ID, Name: p.Name, ProviderType: p.ProviderType, Enabled: p.Enabled, Meta: meta, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}, nil
}

// DeleteDNSProvider refuses to remove a provider still used by certificates.
func (m *Manager) DeleteDNSProvider(ctx context.Context, id uint) error {
	n, err := m.store.CountCertificatesUsingDNSProvider(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return apperr.Conflictf("DNS provider is used by %d certificate(s)", n)
	}
	if err := m.store.DeleteDNSProvider(ctx, id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return apperr.NotFoundf("DNS provider %d not found", id)
		}
		return err
	}
	return nil
}

// ACMEAccountView is the API representation of an account.
type ACMEAccountView struct {
	ID        uint      `json:"id"`
	Email     string    `json:"email"`
	CAURL     string    `json:"ca_url"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// ListACMEAccounts returns accounts without key paths.
func (m *Manager) ListACMEAccounts(ctx context.Context) ([]ACMEAccountView, error) {
	rows, err := m.store.ListACMEAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ACMEAccountView, 0, len(rows))
	for _, a := range rows {
		out = append(out, ACMEAccountView{ID: a.ID, Email: a.Email, CAURL: a.CAURL, Status: a.Status, CreatedAt: a.CreatedAt})
	}
	return out, nil
}

// DeleteACMEAccount removes an unused account and its key file.
func (m *Manager) DeleteACMEAccount(ctx context.Context, id uint) error {
	n, err := m.store.CountCertificatesUsingAccount(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return apperr.Conflictf("ACME account is used by %d certificate(s)", n)
	}
	acc, err := m.store.GetACMEAccount(ctx, id)
	if err != nil {
		return apperr.NotFoundf("ACME account %d not found", id)
	}
	if err := m.store.DeleteACMEAccount(ctx, id); err != nil {
		return err
	}
	if acc.AccountKeyPath != "" {
		_ = os.Remove(m.dataPath(acc.AccountKeyPath))
	}
	return nil
}
