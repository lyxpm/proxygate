package site

import (
	"context"
	"time"

	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/storage"
)

// Export is a portable site configuration. Names are included so an import can
// resolve applications and certificates when IDs differ.
type Export struct {
	Name            string    `json:"name"`
	Enabled         bool      `json:"enabled"`
	ListenPort      int       `json:"listen_port"`
	Domains         []string  `json:"domains"`
	Protocol        string    `json:"protocol"`
	ApplicationID   uint      `json:"application_id"`
	ApplicationName string    `json:"application_name"`
	CertificateID   *uint     `json:"certificate_id"`
	CertificateName string    `json:"certificate_name"`
	RedirectHTTPS   bool      `json:"redirect_https"`
	ProxyTimeout    int       `json:"proxy_timeout"`
	MaxBodySize     int64     `json:"max_body_size"`
	Remark          string    `json:"remark"`
	Options         Options   `json:"options"`
	ExportedAt      time.Time `json:"exported_at"`
}

// ImportInput accepts an exported site. Application and certificate may be
// resolved by ID or by name.
type ImportInput struct {
	Name            string   `json:"name"`
	Enabled         *bool    `json:"enabled"`
	ListenPort      int      `json:"listen_port"`
	Domains         []string `json:"domains"`
	Protocol        string   `json:"protocol"`
	ApplicationID   *uint    `json:"application_id"`
	ApplicationName string   `json:"application_name"`
	CertificateID   *uint    `json:"certificate_id"`
	CertificateName string   `json:"certificate_name"`
	RedirectHTTPS   bool     `json:"redirect_https"`
	ProxyTimeout    int      `json:"proxy_timeout"`
	MaxBodySize     int64    `json:"max_body_size"`
	Remark          string   `json:"remark"`
	Options         Options  `json:"options"`
}

// Export returns a site as portable JSON.
func (s *Service) Export(ctx context.Context, id uint) (*Export, error) {
	row, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	opts, err := DecodeOptions(row.ExtraConfig)
	if err != nil {
		return nil, apperr.Internalf(err, "site %d has invalid options", id)
	}
	out := &Export{
		Name: row.Name, Enabled: row.Enabled, ListenPort: row.ListenPort, Domains: row.Domains,
		Protocol: row.Protocol, ApplicationID: row.ApplicationID, CertificateID: row.CertificateID,
		RedirectHTTPS: row.RedirectHTTPS, ProxyTimeout: row.ProxyTimeout, MaxBodySize: row.MaxBodySize,
		Remark: row.Remark, Options: opts, ExportedAt: time.Now(),
	}
	if row.Application != nil {
		out.ApplicationName = row.Application.Name
	}
	if row.Certificate != nil {
		out.CertificateName = row.Certificate.Name
	}
	return out, nil
}

// Import creates a site from an exported configuration.
func (s *Service) Import(ctx context.Context, in ImportInput) (*storage.Site, error) {
	upID, err := s.resolveApplication(ctx, in)
	if err != nil {
		return nil, err
	}
	certID, err := s.resolveCertificate(ctx, in)
	if err != nil {
		return nil, err
	}
	input := Input{
		Name: in.Name, Enabled: in.Enabled, ListenPort: in.ListenPort, Domains: in.Domains,
		Protocol: in.Protocol, ApplicationID: upID, CertificateID: certID,
		RedirectHTTPS: in.RedirectHTTPS, ProxyTimeout: in.ProxyTimeout, MaxBodySize: in.MaxBodySize,
		Remark: in.Remark, Options: in.Options,
	}
	return s.Create(ctx, input)
}

func (s *Service) resolveApplication(ctx context.Context, in ImportInput) (uint, error) {
	if in.ApplicationName != "" {
		rows, err := s.store.ListApplications(ctx)
		if err != nil {
			return 0, err
		}
		for _, up := range rows {
			if up.Name == in.ApplicationName {
				return up.ID, nil
			}
		}
	}
	if in.ApplicationID != nil && *in.ApplicationID != 0 {
		return *in.ApplicationID, nil
	}
	return 0, apperr.InvalidField("application_id", "application is required")
}

func (s *Service) resolveCertificate(ctx context.Context, in ImportInput) (*uint, error) {
	if in.CertificateName != "" {
		rows, err := s.store.ListCertificates(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range rows {
			if c.Name == in.CertificateName {
				id := c.ID
				return &id, nil
			}
		}
	}
	if in.CertificateID != nil && *in.CertificateID != 0 {
		return in.CertificateID, nil
	}
	return nil, nil
}
