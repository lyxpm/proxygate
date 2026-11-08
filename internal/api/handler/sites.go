package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/site"
	"github.com/proxygate/proxygate/internal/storage"
)

type siteView struct {
	ID              uint         `json:"id"`
	Name            string       `json:"name"`
	Enabled         bool         `json:"enabled"`
	ListenPort      int          `json:"listen_port"`
	Domains         []string     `json:"domains"`
	Protocol        string       `json:"protocol"`
	ApplicationID   uint         `json:"application_id"`
	ApplicationName string       `json:"application_name"`
	CertificateID   *uint        `json:"certificate_id"`
	CertificateName string       `json:"certificate_name"`
	CertExpiresAt   *time.Time   `json:"certificate_expires_at"`
	RedirectHTTPS   bool         `json:"redirect_https"`
	ProxyTimeout    int          `json:"proxy_timeout"`
	MaxBodySize     int64        `json:"max_body_size"`
	Remark          string       `json:"remark"`
	Options         site.Options `json:"options"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

func toSiteView(s storage.Site) siteView {
	opts, err := site.DecodeOptions(s.ExtraConfig)
	if err != nil {
		opts = site.DefaultOptions()
	}
	v := siteView{
		ID: s.ID, Name: s.Name, Enabled: s.Enabled, ListenPort: s.ListenPort,
		Domains: s.Domains, Protocol: s.Protocol, ApplicationID: s.ApplicationID,
		CertificateID: s.CertificateID, RedirectHTTPS: s.RedirectHTTPS,
		ProxyTimeout: s.ProxyTimeout, MaxBodySize: s.MaxBodySize, Remark: s.Remark,
		Options: opts, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
	if s.Application != nil {
		v.ApplicationName = s.Application.Name
	}
	if s.Certificate != nil {
		v.CertificateName = s.Certificate.Name
		v.CertExpiresAt = s.Certificate.ExpiresAt
	}
	return v
}

// ListSites returns sites with optional search and pagination.
func (s *Server) ListSites(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Sites.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	enabled := boolQuery(r, "enabled")
	filtered := make([]siteView, 0, len(rows))
	for _, row := range rows {
		if enabled != nil && row.Enabled != *enabled {
			continue
		}
		if q != "" {
			hit := strings.Contains(strings.ToLower(row.Name), q)
			for _, d := range row.Domains {
				if strings.Contains(strings.ToLower(d), q) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		filtered = append(filtered, toSiteView(row))
	}
	page, pageSize := response.PageQuery(r)
	total := int64(len(filtered))
	start := (page - 1) * pageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	response.OK(w, response.Page{Items: filtered[start:end], Total: total, Page: page, PageSize: pageSize})
}

// CreateSite creates a virtual host.
func (s *Server) CreateSite(w http.ResponseWriter, r *http.Request) {
	var in site.Input
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	row, err := s.Sites.Create(r.Context(), in)
	if err != nil {
		s.audit(r, "create_site", "site", nil, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "create_site", "site", row.ID, "success", map[string]any{"name": row.Name})
	response.Created(w, toSiteView(*row))
}

// GetSite returns one site.
func (s *Server) GetSite(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	row, err := s.Sites.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, toSiteView(*row))
}

// UpdateSite saves a site.
func (s *Server) UpdateSite(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in site.Input
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	row, err := s.Sites.Update(r.Context(), id, in)
	if err != nil {
		s.audit(r, "update_site", "site", id, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "update_site", "site", id, "success", map[string]any{"name": row.Name})
	response.OK(w, toSiteView(*row))
}

// DeleteSite removes a site.
func (s *Server) DeleteSite(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Sites.Delete(r.Context(), id); err != nil {
		s.audit(r, "delete_site", "site", id, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "delete_site", "site", id, "success", nil)
	w.WriteHeader(http.StatusNoContent)
}

// SetSiteEnabled toggles a site.
func (s *Server) SetSiteEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	row, err := s.Sites.SetEnabled(r.Context(), id, enabled)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	action := "disable_site"
	if enabled {
		action = "enable_site"
	}
	s.audit(r, action, "site", id, "success", nil)
	response.OK(w, toSiteView(*row))
}

// EnableSite handles POST /sites/:id/enable.
func (s *Server) EnableSite(w http.ResponseWriter, r *http.Request) { s.SetSiteEnabled(w, r, true) }

// DisableSite handles POST /sites/:id/disable.
func (s *Server) DisableSite(w http.ResponseWriter, r *http.Request) { s.SetSiteEnabled(w, r, false) }

// SiteStatistics returns totals and a time series for one site.
func (s *Server) SiteStatistics(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	row, err := s.Sites.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	from, to, step, err := rangeParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.Stats.BySite(r.Context(), from, to)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var st responseSiteTotals
	for _, t := range rows {
		if t.SiteID == id {
			st = responseSiteTotals{Requests: t.Requests, Inbound: t.Inbound, Outbound: t.Outbound, AvgMS: t.AvgMS, Status2xx: t.Status2xx, Status3xx: t.Status3xx, Status4xx: t.Status4xx, Status5xx: t.Status5xx}
			break
		}
	}
	series, err := s.Stats.Series(r.Context(), id, from, to, step)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	topIPs, topPaths := s.Access.Top(id, from, to, 10)
	response.OK(w, map[string]any{
		"site": toSiteView(*row), "totals": st, "series": series,
		"top_ips": topIPs, "top_paths": topPaths,
		"range": map[string]any{"from": from, "to": to},
	})
}

type responseSiteTotals struct {
	Requests  int64 `json:"requests"`
	Inbound   int64 `json:"inbound_bytes"`
	Outbound  int64 `json:"outbound_bytes"`
	AvgMS     int64 `json:"avg_ms"`
	Status2xx int64 `json:"status_2xx"`
	Status3xx int64 `json:"status_3xx"`
	Status4xx int64 `json:"status_4xx"`
	Status5xx int64 `json:"status_5xx"`
}

// ExportSite returns a portable site configuration.
func (s *Server) ExportSite(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data, err := s.Sites.Export(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, data)
}

// ImportSite creates a site from an exported configuration.
func (s *Server) ImportSite(w http.ResponseWriter, r *http.Request) {
	var in site.ImportInput
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	row, err := s.Sites.Import(r.Context(), in)
	if err != nil {
		s.audit(r, "import_site", "site", nil, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "import_site", "site", row.ID, "success", map[string]any{"name": row.Name})
	response.Created(w, toSiteView(*row))
}

// SiteLogs returns recent access log entries for one site.
func (s *Server) SiteLogs(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.Sites.Get(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	page, pageSize := response.PageQuery(r)
	items, total := s.Access.Query(accessFilter(r, id, page, pageSize))
	response.OK(w, response.Page{Items: items, Total: total, Page: page, PageSize: pageSize})
}
