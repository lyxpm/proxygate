package handler

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/proxygate/proxygate/internal/api/response"
)

// StatisticsOverview returns totals and a series.
func (s *Server) StatisticsOverview(w http.ResponseWriter, r *http.Request) {
	from, to, step, err := rangeParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	totals, err := s.Stats.Totals(r.Context(), from, to)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	series, err := s.Stats.Series(r.Context(), 0, from, to, step)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, map[string]any{"totals": totals, "series": series, "from": from, "to": to})
}

// StatisticsTraffic returns a series for one site or all sites.
func (s *Server) StatisticsTraffic(w http.ResponseWriter, r *http.Request) {
	from, to, step, err := rangeParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var siteID uint
	if v := r.URL.Query().Get("site_id"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		siteID = uint(n)
	}
	series, err := s.Stats.Series(r.Context(), siteID, from, to, step)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, map[string]any{"series": series, "from": from, "to": to})
}

type siteTrafficItem struct {
	SiteID    uint     `json:"site_id"`
	SiteName  string   `json:"site_name"`
	Domains   []string `json:"domains"`
	Requests  int64    `json:"requests"`
	Inbound   int64    `json:"inbound_bytes"`
	Outbound  int64    `json:"outbound_bytes"`
	AvgMS     int64    `json:"avg_ms"`
	Status2xx int64    `json:"status_2xx"`
	Status3xx int64    `json:"status_3xx"`
	Status4xx int64    `json:"status_4xx"`
	Status5xx int64    `json:"status_5xx"`
}

// StatisticsSites ranks sites by requests.
func (s *Server) StatisticsSites(w http.ResponseWriter, r *http.Request) {
	from, to, _, err := rangeParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.Stats.BySite(r.Context(), from, to)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sites, err := s.Sites.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names := make(map[uint]siteTrafficItem, len(sites))
	for _, site := range sites {
		names[site.ID] = siteTrafficItem{SiteID: site.ID, SiteName: site.Name, Domains: site.Domains}
	}
	items := make([]siteTrafficItem, 0, len(rows))
	for _, t := range rows {
		item := names[t.SiteID]
		item.SiteID = t.SiteID
		item.Requests = t.Requests
		item.Inbound = t.Inbound
		item.Outbound = t.Outbound
		item.AvgMS = t.AvgMS
		item.Status2xx = t.Status2xx
		item.Status3xx = t.Status3xx
		item.Status4xx = t.Status4xx
		item.Status5xx = t.Status5xx
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Requests > items[j].Requests })
	response.OK(w, map[string]any{"items": items, "from": from, "to": to})
}

// StatisticsStatusCodes returns the status distribution.
func (s *Server) StatisticsStatusCodes(w http.ResponseWriter, r *http.Request) {
	from, to, _, err := rangeParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	totals, err := s.Stats.Totals(r.Context(), from, to)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, map[string]any{
		"totals": totals,
		"items": []map[string]any{
			{"class": "2xx", "count": totals.Status2xx},
			{"class": "3xx", "count": totals.Status3xx},
			{"class": "4xx", "count": totals.Status4xx},
			{"class": "5xx", "count": totals.Status5xx},
		},
	})
}

// StatisticsApplications returns per-node runtime counters.
func (s *Server) StatisticsApplications(w http.ResponseWriter, r *http.Request) {
	status := s.Engine.Status()
	response.OK(w, map[string]any{"applications": status.Applications})
}
