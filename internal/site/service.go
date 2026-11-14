// Package site implements virtual-host business rules: validation, domain
// conflict detection, certificate coverage and persistence.
package site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/storage"
)

// Header is one header rule.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Options is stored as JSON in sites.extra_config.
type Options struct {
	StripPrefix            string   `json:"stripPrefix"`
	AddPrefix              string   `json:"addPrefix"`
	RequestHeaders         []Header `json:"requestHeaders"`
	ResponseHeaders        []Header `json:"responseHeaders"`
	UpstreamSNI            string   `json:"upstreamSni"`
	UpstreamTLSVerify      *bool    `json:"upstreamTlsVerify,omitempty"`
	KeepAlive              *bool    `json:"keepAlive,omitempty"`
	UseUpstreamHost        bool     `json:"useUpstreamHost"`
	ForwardClientIP        *bool    `json:"forwardClientIp,omitempty"`
	BufferSize             int      `json:"bufferSize"`
	IPMode                 string   `json:"ipMode"`
	IPList                 []string `json:"ipList"`
	ConnectionLimitEnabled bool     `json:"connectionLimitEnabled"`
	ConnectionLimit        int      `json:"connectionLimit"`
	RateLimitEnabled       bool     `json:"rateLimitEnabled"`
	RateLimitPerSecond     int      `json:"rateLimitPerSecond"`
	RateLimitBurst         int      `json:"rateLimitBurst"`
}

// DefaultOptions returns the defaults applied to new sites.
func DefaultOptions() Options {
	t := true
	return Options{UpstreamTLSVerify: &t, KeepAlive: &t, ForwardClientIP: &t, BufferSize: 32 * 1024}
}

// Input is the create/update payload.
type Input struct {
	Name            string   `json:"name"`
	Enabled         *bool    `json:"enabled"`
	ListenPort      int      `json:"listen_port"`
	Domains         []string `json:"domains"`
	Protocol        string   `json:"protocol"`
	ApplicationID   uint     `json:"application_id"`
	ApplicationCode string   `json:"application_code"`
	CertificateID   *uint    `json:"certificate_id"`
	CertificateCode *string  `json:"certificate_code"`
	RedirectHTTPS   bool     `json:"redirect_https"`
	ProxyTimeout    int      `json:"proxy_timeout"`
	MaxBodySize     int64    `json:"max_body_size"`
	Remark          string   `json:"remark"`
	Options         Options  `json:"options"`
}

// Service applies site rules.
type Service struct {
	store  *storage.Store
	reload func(context.Context) error
}

// NewService creates a site service.
func NewService(store *storage.Store, reload func(context.Context) error) *Service {
	return &Service{store: store, reload: reload}
}

// List returns all sites with associations.
func (s *Service) List(ctx context.Context) ([]storage.Site, error) {
	return s.store.ListSites(ctx)
}

// Get returns one site.
func (s *Service) Get(ctx context.Context, id uint) (*storage.Site, error) {
	site, err := s.store.GetSite(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, apperr.NotFoundf("site %d not found", id)
	}
	return site, err
}

// GetByCode returns one site by its immutable business code.
func (s *Service) GetByCode(ctx context.Context, code string) (*storage.Site, error) {
	site, err := s.store.GetSiteByCode(ctx, code)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, apperr.NotFoundf("site %s not found", code)
	}
	return site, err
}

// Create validates and inserts a site.
func (s *Service) Create(ctx context.Context, in Input) (*storage.Site, error) {
	sites, err := s.store.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	site, err := s.build(ctx, in, nil, sites)
	if err != nil {
		return nil, err
	}
	if err := s.store.CreateSite(ctx, site); err != nil {
		return nil, mapDBError(err)
	}
	if err := s.reload(ctx); err != nil {
		_ = s.store.DeleteSite(ctx, site.ID)
		_ = s.reload(ctx)
		return nil, apperr.Internalf(err, "site was not applied")
	}
	return s.store.GetSite(ctx, site.ID)
}

// Update validates and saves a site.
func (s *Service) Update(ctx context.Context, id uint, in Input) (*storage.Site, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	sites, err := s.store.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	site, err := s.build(ctx, in, current, sites)
	if err != nil {
		return nil, err
	}
	site.ID = current.ID
	site.CreatedAt = current.CreatedAt
	if err := s.store.UpdateSite(ctx, site); err != nil {
		return nil, mapDBError(err)
	}
	if err := s.reload(ctx); err != nil {
		// Do not leave the database ahead of the serving runtime.
		_ = s.store.UpdateSite(ctx, current)
		_ = s.reload(ctx)
		return nil, apperr.Internalf(err, "site saved but runtime update failed")
	}
	return s.store.GetSite(ctx, id)
}

// Delete removes a site.
func (s *Service) Delete(ctx context.Context, id uint) error {
	site, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteSite(ctx, id); err != nil {
		return err
	}
	if err := s.reload(ctx); err != nil {
		if restoreErr := s.store.CreateSite(ctx, site); restoreErr != nil {
			return apperr.Internalf(err, "site deleted but runtime update and restore failed: %s", restoreErr)
		}
		_ = s.reload(ctx)
		return apperr.Internalf(err, "site deleted but runtime update failed")
	}
	return nil
}

// SetEnabled toggles a site and reapplies the runtime.
func (s *Service) SetEnabled(ctx context.Context, id uint, enabled bool) (*storage.Site, error) {
	site, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	previous := site.Enabled
	site.Enabled = enabled
	if err := s.store.UpdateSite(ctx, site); err != nil {
		return nil, err
	}
	if err := s.reload(ctx); err != nil {
		site.Enabled = previous
		_ = s.store.UpdateSite(ctx, site)
		_ = s.reload(ctx)
		return nil, apperr.Internalf(err, "site saved but runtime update failed")
	}
	return s.store.GetSite(ctx, id)
}

func (s *Service) build(ctx context.Context, in Input, current *storage.Site, all []storage.Site) (*storage.Site, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperr.InvalidField("name", "name is required")
	}
	if len(name) > 128 {
		return nil, apperr.InvalidField("name", "name is too long")
	}
	protocol := strings.ToLower(strings.TrimSpace(in.Protocol))
	if protocol == "" {
		protocol = "http"
	}
	if protocol != "http" && protocol != "https" {
		return nil, apperr.InvalidField("protocol", "protocol must be http or https")
	}
	port := in.ListenPort
	if port == 0 {
		if protocol == "https" {
			port = 443
		} else {
			port = 80
		}
	}
	if port < 1 || port > 65535 {
		return nil, apperr.InvalidField("listen_port", "listen port must be between 1 and 65535")
	}
	if in.RedirectHTTPS && protocol != "https" {
		return nil, apperr.InvalidField("redirect_https", "HTTPS redirect requires an HTTPS site")
	}
	if in.RedirectHTTPS && port != 443 {
		return nil, apperr.InvalidField("redirect_https", "HTTPS redirect is only supported on port 443")
	}
	domains, err := normalizeDomains(in.Domains)
	if err != nil {
		return nil, err
	}
	if len(domains) == 0 {
		return nil, apperr.InvalidField("domains", "at least one domain is required")
	}
	if code := strings.TrimSpace(in.ApplicationCode); code != "" {
		up, err := s.store.GetApplicationByCode(ctx, code)
		if errors.Is(err, storage.ErrNotFound) {
			return nil, apperr.InvalidField("application_code", "selected application does not exist")
		}
		if err != nil {
			return nil, err
		}
		in.ApplicationID = up.ID
	}
	if in.ApplicationID == 0 {
		return nil, apperr.InvalidField("application_id", "application is required")
	}
	up, err := s.store.GetApplication(ctx, in.ApplicationID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, apperr.InvalidField("application_id", "selected application does not exist")
	}
	if err != nil {
		return nil, err
	}
	if !up.Enabled {
		return nil, apperr.InvalidField("application_id", "selected application is disabled")
	}
	usable := 0
	for _, n := range up.Nodes {
		if n.Enabled {
			usable++
		}
	}
	if usable == 0 {
		return nil, apperr.InvalidField("application_id", "selected application has no enabled nodes")
	}
	var cert *storage.Certificate
	if in.CertificateCode != nil && strings.TrimSpace(*in.CertificateCode) != "" {
		cert, err = s.store.GetCertificateByCode(ctx, strings.TrimSpace(*in.CertificateCode))
		if errors.Is(err, storage.ErrNotFound) {
			return nil, apperr.InvalidField("certificate_code", "selected certificate does not exist")
		}
		if err != nil {
			return nil, err
		}
		in.CertificateID = &cert.ID
	}
	if cert == nil && in.CertificateID != nil && *in.CertificateID != 0 {
		cert, err = s.store.GetCertificate(ctx, *in.CertificateID)
		if errors.Is(err, storage.ErrNotFound) {
			return nil, apperr.InvalidField("certificate_id", "selected certificate does not exist")
		}
		if err != nil {
			return nil, err
		}
	}
	if protocol == "https" {
		if cert == nil {
			return nil, apperr.InvalidField("certificate_id", "an HTTPS site requires a certificate")
		}
		if cert.Status != "valid" {
			return nil, apperr.InvalidField("certificate_id", "certificate is not valid yet")
		}
		if cert.ExpiresAt != nil && cert.ExpiresAt.Before(time.Now()) {
			return nil, apperr.InvalidField("certificate_id", "certificate has expired")
		}
		for _, d := range domains {
			if !certificateCovers(cert.Domains, d) {
				return nil, apperr.InvalidField("certificate_id", fmt.Sprintf("certificate does not cover %s", d))
			}
		}
	} else if cert != nil {
		return nil, apperr.InvalidField("certificate_id", "HTTP sites cannot use a certificate")
	}
	timeout := in.ProxyTimeout
	if timeout < 0 || timeout > 3600 {
		return nil, apperr.InvalidField("proxy_timeout", "proxy timeout must be between 0 and 3600 seconds")
	}
	if in.MaxBodySize < 0 || in.MaxBodySize > 1<<40 {
		return nil, apperr.InvalidField("max_body_size", "max body size must be between 0 and 1 TiB")
	}
	opts, err := normalizeOptions(in.Options)
	if err != nil {
		return nil, err
	}
	extra, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}
	if err := checkConflicts(all, current, domains, port, protocol); err != nil {
		return nil, err
	}
	site := &storage.Site{
		Name: name, Enabled: boolOr(in.Enabled, true), ListenPort: port,
		Domains: domains, Protocol: protocol, ApplicationID: in.ApplicationID,
		CertificateID: in.CertificateID, RedirectHTTPS: in.RedirectHTTPS,
		ProxyTimeout: timeout, MaxBodySize: in.MaxBodySize,
		ExtraConfig: string(extra), Remark: strings.TrimSpace(in.Remark),
	}
	if current != nil {
		site.Code = current.Code
		site.CreatedAt = current.CreatedAt
	}
	return site, nil
}

// DecodeOptions reads options with defaults for missing fields.
func DecodeOptions(raw string) (Options, error) {
	opts := DefaultOptions()
	if strings.TrimSpace(raw) == "" {
		return opts, nil
	}
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		return opts, err
	}
	if opts.BufferSize == 0 {
		opts.BufferSize = 32 * 1024
	}
	if opts.UpstreamTLSVerify == nil {
		t := true
		opts.UpstreamTLSVerify = &t
	}
	if opts.KeepAlive == nil {
		t := true
		opts.KeepAlive = &t
	}
	if opts.ForwardClientIP == nil {
		t := true
		opts.ForwardClientIP = &t
	}
	return opts, nil
}

func normalizeOptions(in Options) (Options, error) {
	out := DefaultOptions()
	out.StripPrefix = strings.TrimSpace(in.StripPrefix)
	out.AddPrefix = strings.TrimSpace(in.AddPrefix)
	out.UpstreamSNI = strings.TrimSpace(in.UpstreamSNI)
	out.UseUpstreamHost = in.UseUpstreamHost
	out.BufferSize = in.BufferSize
	if out.BufferSize == 0 {
		out.BufferSize = 32 * 1024
	}
	if out.BufferSize < 1024 || out.BufferSize > 1<<20 {
		return out, apperr.InvalidField("options.bufferSize", "buffer size must be between 1 KiB and 1 MiB")
	}
	if out.StripPrefix != "" && !strings.HasPrefix(out.StripPrefix, "/") {
		return out, apperr.InvalidField("options.stripPrefix", "strip prefix must start with /")
	}
	if out.AddPrefix != "" && !strings.HasPrefix(out.AddPrefix, "/") {
		return out, apperr.InvalidField("options.addPrefix", "add prefix must start with /")
	}
	if in.UpstreamTLSVerify != nil {
		out.UpstreamTLSVerify = in.UpstreamTLSVerify
	}
	if in.KeepAlive != nil {
		out.KeepAlive = in.KeepAlive
	}
	if in.ForwardClientIP != nil {
		out.ForwardClientIP = in.ForwardClientIP
	}
	for _, h := range append(append([]Header{}, in.RequestHeaders...), in.ResponseHeaders...) {
		if !validHeaderName(h.Name) {
			return out, apperr.InvalidField("options", fmt.Sprintf("invalid header name %q", h.Name))
		}
		if strings.ContainsAny(h.Value, "\r\n") {
			return out, apperr.InvalidField("options", "header values must not contain newlines")
		}
	}
	out.RequestHeaders = in.RequestHeaders
	out.ResponseHeaders = in.ResponseHeaders
	mode := strings.ToLower(strings.TrimSpace(in.IPMode))
	if mode != "" && mode != "allow" && mode != "deny" {
		return out, apperr.InvalidField("options.ipMode", "IP mode must be allow or deny")
	}
	out.IPMode = mode
	out.IPList = nil
	seen := make(map[string]bool)
	for _, raw := range in.IPList {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		cidr, err := normalizeCIDR(raw)
		if err != nil {
			return out, apperr.InvalidField("options.ipList", fmt.Sprintf("invalid IP or CIDR %q", raw))
		}
		if !seen[cidr] {
			seen[cidr] = true
			out.IPList = append(out.IPList, cidr)
		}
	}
	if mode != "" && len(out.IPList) == 0 {
		return out, apperr.InvalidField("options.ipList", "IP access control requires at least one IP or CIDR")
	}
	out.ConnectionLimitEnabled = in.ConnectionLimitEnabled
	out.ConnectionLimit = 0
	if out.ConnectionLimitEnabled {
		if in.ConnectionLimit < 1 || in.ConnectionLimit > 100000 {
			return out, apperr.InvalidField("options.connectionLimit", "connection limit must be between 1 and 100000")
		}
		out.ConnectionLimit = in.ConnectionLimit
	}
	out.RateLimitEnabled = in.RateLimitEnabled
	out.RateLimitPerSecond = 0
	out.RateLimitBurst = 0
	if out.RateLimitEnabled {
		if in.RateLimitPerSecond < 1 || in.RateLimitPerSecond > 100000 {
			return out, apperr.InvalidField("options.rateLimitPerSecond", "rate limit must be between 1 and 100000")
		}
		if in.RateLimitBurst < 1 || in.RateLimitBurst > 100000 {
			return out, apperr.InvalidField("options.rateLimitBurst", "burst must be between 1 and 100000")
		}
		out.RateLimitPerSecond = in.RateLimitPerSecond
		out.RateLimitBurst = in.RateLimitBurst
	}
	return out, nil
}

func normalizeCIDR(raw string) (string, error) {
	if strings.Contains(raw, "/") {
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return "", err
		}
		return network.String(), nil
	}
	ip := net.ParseIP(raw)
	if ip == nil {
		return "", fmt.Errorf("invalid IP")
	}
	if ip.To4() != nil {
		return ip.String() + "/32", nil
	}
	return ip.String() + "/128", nil
}

var headerName = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)

func validHeaderName(name string) bool { return headerName.MatchString(strings.TrimSpace(name)) }

var domainRE = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

func normalizeDomains(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool)
	for _, raw := range in {
		d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if d == "" {
			continue
		}
		if d != "*" && net.ParseIP(d) == nil && (len(d) > 253 || !domainRE.MatchString(d)) {
			return nil, apperr.InvalidField("domains", fmt.Sprintf("invalid domain %q", raw))
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out, nil
}

func checkConflicts(all []storage.Site, current *storage.Site, domains []string, port int, protocol string) error {
	for _, other := range all {
		if current != nil && other.ID == current.ID {
			continue
		}
		if other.ListenPort != port {
			continue
		}
		if other.Protocol != protocol && other.Enabled {
			return apperr.Conflictf("port %d is already used by %s sites", port, other.Protocol)
		}
		if !other.Enabled {
			continue
		}
		for _, a := range domains {
			for _, b := range other.Domains {
				if domainsConflict(a, b) {
					return apperr.Conflictf("domain %s conflicts with site %q", a, other.Name)
				}
			}
		}
	}
	return nil
}

// domainsConflict reports whether two patterns cannot coexist unambiguously.
// Exact and wildcard overlap is allowed because exact matches win at runtime.
func domainsConflict(a, b string) bool {
	if a == b {
		return true
	}
	if a == "*" || b == "*" {
		return true
	}
	return false
}

func certificateCovers(patterns []string, host string) bool {
	host = strings.ToLower(host)
	for _, raw := range patterns {
		p := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if p == host {
			return true
		}
		if strings.HasPrefix(p, "*.") {
			root := strings.TrimPrefix(p, "*.")
			if i := strings.IndexByte(host, '.'); i > 0 && host[i+1:] == root {
				return true
			}
		}
	}
	return false
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func mapDBError(err error) error {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unique constraint") {
		return apperr.Conflictf("a site with this name already exists")
	}
	if strings.Contains(msg, "foreign key") {
		return apperr.Conflictf("site references a resource that cannot be changed")
	}
	return err
}
