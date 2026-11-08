package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/certificate"
	"github.com/proxygate/proxygate/internal/certificate/provider"
	"github.com/proxygate/proxygate/internal/storage"
)

type certView struct {
	ID              uint       `json:"id"`
	Name            string     `json:"name"`
	Domains         []string   `json:"domains"`
	Issuer          string     `json:"issuer"`
	CertificateType string     `json:"certificate_type"`
	ChallengeType   string     `json:"challenge_type"`
	Status          string     `json:"status"`
	AutoRenew       bool       `json:"auto_renew"`
	ExpiresAt       *time.Time `json:"expires_at"`
	DaysRemaining   *int       `json:"days_remaining"`
	LastRenewAt     *time.Time `json:"last_renew_at"`
	NextRenewAt     *time.Time `json:"next_renew_at"`
	RenewAttempts   int        `json:"renew_attempts"`
	LastError       string     `json:"last_error"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func toCertView(c storage.Certificate) certView {
	v := certView{
		ID: c.ID, Name: c.Name, Domains: c.Domains, Issuer: c.Issuer,
		CertificateType: c.CertificateType, ChallengeType: c.ChallengeType,
		Status: c.Status, AutoRenew: c.AutoRenew, ExpiresAt: c.ExpiresAt,
		LastRenewAt: c.LastRenewAt, NextRenewAt: c.NextRenewAt,
		RenewAttempts: c.RenewAttempts, LastError: c.LastError,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if c.ExpiresAt != nil {
		days := int(time.Until(*c.ExpiresAt).Hours() / 24)
		v.DaysRemaining = &days
	}
	return v
}

// ListCertificates returns all certificates.
func (s *Server) ListCertificates(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Certs.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	status := r.URL.Query().Get("status")
	items := make([]certView, 0, len(rows))
	for _, row := range rows {
		if status != "" && row.Status != status {
			continue
		}
		if q != "" {
			hit := strings.Contains(strings.ToLower(row.Name), q)
			for _, d := range row.Domains {
				if strings.Contains(strings.ToLower(d), q) {
					hit = true
				}
			}
			if !hit {
				continue
			}
		}
		items = append(items, toCertView(row))
	}
	page, pageSize := response.PageQuery(r)
	total := int64(len(items))
	start := (page - 1) * pageSize
	if start > len(items) {
		start = len(items)
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	response.OK(w, response.Page{Items: items[start:end], Total: total, Page: page, PageSize: pageSize})
}

// CreateACMECertificate starts an asynchronous ACME order.
func (s *Server) CreateACMECertificate(w http.ResponseWriter, r *http.Request) {
	var in certificate.IssueRequest
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	cert, err := s.Certs.CreateIssue(r.Context(), in)
	if err != nil {
		s.audit(r, "issue_certificate", "certificate", nil, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "issue_certificate", "certificate", cert.ID, "success", map[string]any{"domains": cert.Domains, "challenge": cert.ChallengeType})
	go func(id uint) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		if err := s.Certs.RunIssue(ctx, id); err != nil {
			s.Logger.Warn("ACME issuance failed", "certificate", id, "error", err)
		}
	}(cert.ID)
	response.JSON(w, http.StatusAccepted, 0, "accepted", toCertView(*cert))
}

// UploadCertificate stores a PEM pair.
func (s *Server) UploadCertificate(w http.ResponseWriter, r *http.Request) {
	var in certificate.UploadRequest
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	cert, err := s.Certs.Upload(r.Context(), in)
	if err != nil {
		s.audit(r, "upload_certificate", "certificate", nil, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "upload_certificate", "certificate", cert.ID, "success", map[string]any{"domains": cert.Domains})
	response.Created(w, toCertView(*cert))
}

// GetCertificate returns one certificate.
func (s *Server) GetCertificate(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cert, err := s.Certs.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, toCertView(*cert))
}

// DeleteCertificate removes a certificate.
func (s *Server) DeleteCertificate(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Certs.Delete(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "delete_certificate", "certificate", id, "success", nil)
	w.WriteHeader(http.StatusNoContent)
}

// RenewCertificate triggers an asynchronous renewal.
func (s *Server) RenewCertificate(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cert, err := s.Certs.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if cert.CertificateType != "acme" {
		s.fail(w, r, apperr.Invalidf("only ACME certificates can be renewed"))
		return
	}
	s.audit(r, "renew_certificate", "certificate", id, "success", nil)
	go func(cid uint) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		if err := s.Certs.RenewNow(ctx, cid); err != nil {
			s.Logger.Warn("certificate renewal failed", "certificate", cid, "error", err)
		}
	}(id)
	response.JSON(w, http.StatusAccepted, 0, "accepted", toCertView(*cert))
}

// CertificateLogs returns renewal log lines.
func (s *Server) CertificateLogs(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	logs, err := s.Certs.Logs(r.Context(), id, 200)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, map[string]any{"items": logs})
}

// ListDNSProviders returns configured providers.
func (s *Server) ListDNSProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Certs.ListDNSProviders(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, map[string]any{"items": rows, "supported": providerList()})
}

// CreateDNSProvider stores a provider.
func (s *Server) CreateDNSProvider(w http.ResponseWriter, r *http.Request) {
	var in certificate.DNSProviderInput
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.Certs.CreateDNSProvider(r.Context(), in)
	if err != nil {
		s.audit(r, "create_dns_provider", "dns_provider", nil, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "create_dns_provider", "dns_provider", p.ID, "success", map[string]any{"name": p.Name, "type": p.ProviderType})
	response.Created(w, p)
}

// UpdateDNSProvider saves a provider.
func (s *Server) UpdateDNSProvider(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in certificate.DNSProviderInput
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.Certs.UpdateDNSProvider(r.Context(), id, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "update_dns_provider", "dns_provider", id, "success", nil)
	response.OK(w, p)
}

// DeleteDNSProvider removes a provider.
func (s *Server) DeleteDNSProvider(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Certs.DeleteDNSProvider(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "delete_dns_provider", "dns_provider", id, "success", nil)
	w.WriteHeader(http.StatusNoContent)
}

// ListACMEAccounts returns accounts.
func (s *Server) ListACMEAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Certs.ListACMEAccounts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, map[string]any{"items": rows})
}

// DeleteACMEAccount removes an account.
func (s *Server) DeleteACMEAccount(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Certs.DeleteACMEAccount(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "delete_acme_account", "acme_account", id, "success", nil)
	w.WriteHeader(http.StatusNoContent)
}

func providerList() []provider.Meta { return provider.List() }
