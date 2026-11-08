package handler

import (
	"net/http"
	"time"

	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/storage"
	"github.com/proxygate/proxygate/internal/system"
)

type countPair struct {
	Total   int `json:"total"`
	Healthy int `json:"healthy"`
}

// DashboardOverview returns the aggregated dashboard payload.
func (s *Server) DashboardOverview(w http.ResponseWriter, r *http.Request) {
	sites, err := s.Sites.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	certs, err := s.Certs.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status := s.Engine.Status()
	healthyApplications := map[uint]bool{}
	healthyNodes := 0
	totalNodes := 0
	for _, up := range status.Applications {
		for _, n := range up.Nodes {
			totalNodes++
			if n.Healthy {
				healthyNodes++
				healthyApplications[up.ID] = true
			}
		}
	}
	enabledSites := 0
	healthySites := 0
	for _, site := range sites {
		if !site.Enabled {
			continue
		}
		enabledSites++
		if healthyApplications[site.ApplicationID] {
			healthySites++
		}
	}
	expiring := 0
	for _, c := range certs {
		if c.ExpiresAt != nil && time.Until(*c.ExpiresAt) <= 30*24*time.Hour {
			expiring++
		}
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	today, err := s.Stats.Totals(r.Context(), midnight, now)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	events, _, err := s.Store.ListSystemLogs(r.Context(), storage.SystemLogFilter{Page: 1, PageSize: 10})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if events == nil {
		events = []storage.SystemLog{}
	}
	response.OK(w, map[string]any{
		"sites":         countPair{Total: len(sites), Healthy: healthySites},
		"enabled_sites": enabledSites,
		"applications":  map[string]any{"nodes_total": totalNodes, "nodes_healthy": healthyNodes, "groups": len(status.Applications)},
		"certificates":  map[string]any{"total": len(certs), "expiring": expiring},
		"connections":   status.ActiveConnections,
		"host":          system.Host(),
		"today": map[string]any{
			"requests": today.Requests, "inbound_bytes": today.Inbound, "outbound_bytes": today.Outbound,
			"avg_ms": today.AvgMS, "p95_ms": today.P95MS, "p99_ms": today.P99MS,
		},
		"status_codes": map[string]int64{
			"2xx": today.Status2xx, "3xx": today.Status3xx, "4xx": today.Status4xx, "5xx": today.Status5xx,
		},
		"recent_events": events,
	})
}

// DashboardTraffic returns a traffic series for the requested range.
func (s *Server) DashboardTraffic(w http.ResponseWriter, r *http.Request) {
	from, to, step, err := rangeParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	series, err := s.Stats.Series(r.Context(), 0, from, to, step)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, map[string]any{"series": series, "from": from, "to": to})
}

// DashboardStatus returns engine and site status.
func (s *Server) DashboardStatus(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]any{"engine": s.Engine.Status()})
}

// DashboardEvents returns recent system events.
func (s *Server) DashboardEvents(w http.ResponseWriter, r *http.Request) {
	page, pageSize := response.PageQuery(r)
	rows, total, err := s.Store.ListSystemLogs(r.Context(), storage.SystemLogFilter{
		Level: r.URL.Query().Get("level"), Category: r.URL.Query().Get("category"),
		Page: page, PageSize: pageSize,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, response.Page{Items: rows, Total: total, Page: page, PageSize: pageSize})
}
