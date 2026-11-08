package handler

import (
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/proxygate/proxygate/internal/api/response"
)

type topoNode struct {
	ID    string         `json:"id"`
	Type  string         `json:"type"`
	Label string         `json:"label"`
	Data  map[string]any `json:"data"`
}

type topoEdge struct {
	ID       string  `json:"id"`
	Source   string  `json:"source"`
	Target   string  `json:"target"`
	Type     string  `json:"type,omitempty"`
	Animated bool    `json:"animated"`
	QPS      float64 `json:"qps"`
}

// Topology builds the configuration-driven network graph for Vue Flow.
func (s *Server) Topology(w http.ResponseWriter, r *http.Request) {
	sites, err := s.Sites.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ups, err := s.Applications.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status := s.Engine.Status()
	live := map[uint]map[uint]bool{}
	for _, up := range status.Applications {
		m := make(map[uint]bool, len(up.Nodes))
		for _, n := range up.Nodes {
			m[n.ID] = n.Healthy
		}
		live[up.ID] = m
	}
	// Live counters and 60-second sparklines come from the in-memory access
	// ring. They are approximate, bounded and intentionally not persisted.
	const points = 60
	const qpsWindow = 60.0
	now := time.Now()
	siteQPS := map[uint]float64{}
	nodeQPS := map[string]float64{}
	siteReq := map[uint][]int64{}
	siteBytes := map[uint][]int64{}
	nodeReq := map[string][]int64{}
	nodeBytes := map[string][]int64{}
	siteIPs := map[uint]map[string]bool{}
	nodeIPs := map[string]map[string]bool{}
	allReq := make([]int64, points)
	allBytes := make([]int64, points)
	allIPs := map[string]bool{}
	newSeries := func() []int64 { return make([]int64, points) }
	if s.Access != nil {
		for _, entry := range s.Access.Since(now.Add(-points * time.Second)) {
			idx := int(entry.Time.Unix() - now.Unix() + points - 1)
			if idx < 0 || idx >= points {
				continue
			}
			siteQPS[entry.SiteID]++
			if siteReq[entry.SiteID] == nil {
				siteReq[entry.SiteID] = newSeries()
				siteBytes[entry.SiteID] = newSeries()
				siteIPs[entry.SiteID] = map[string]bool{}
			}
			siteReq[entry.SiteID][idx]++
			siteBytes[entry.SiteID][idx] += entry.RequestBytes + entry.ResponseBytes
			if entry.ClientIP != "" {
				siteIPs[entry.SiteID][entry.ClientIP] = true
				allIPs[entry.ClientIP] = true
			}
			allReq[idx]++
			allBytes[idx] += entry.RequestBytes + entry.ResponseBytes
			if entry.Application != "" {
				nodeQPS[entry.Application]++
				if nodeReq[entry.Application] == nil {
					nodeReq[entry.Application] = newSeries()
					nodeBytes[entry.Application] = newSeries()
					nodeIPs[entry.Application] = map[string]bool{}
				}
				nodeReq[entry.Application][idx]++
				nodeBytes[entry.Application][idx] += entry.RequestBytes + entry.ResponseBytes
				if entry.ClientIP != "" {
					nodeIPs[entry.Application][entry.ClientIP] = true
				}
			}
		}
	}
	totalQPS := 0.0
	for id, count := range siteQPS {
		siteQPS[id] = math.Round(count/qpsWindow*100) / 100
		totalQPS += siteQPS[id]
	}
	for addr, count := range nodeQPS {
		nodeQPS[addr] = math.Round(count/qpsWindow*100) / 100
	}
	poolQPS := map[uint]float64{}
	for _, site := range sites {
		poolQPS[site.ApplicationID] += siteQPS[site.ID]
	}

	siteActive := map[uint]int64{}
	for _, liveSite := range status.Sites {
		siteActive[liveSite.ID] = liveSite.Active
	}
	nodeActive := map[string]int64{}
	nodeTotal := map[string]int64{}
	for _, up := range status.Applications {
		for _, n := range up.Nodes {
			nodeActive[n.Address] = n.Active
			nodeTotal[n.Address] = n.Requests
		}
	}
	siteTotal := map[uint]int64{}
	if s.Stats != nil {
		if rows, err := s.Stats.LifetimeBySite(r.Context(), now); err == nil {
			for _, row := range rows {
				siteTotal[row.SiteID] = row.RequestCount
			}
		}
	}
	type poolStat struct {
		active int64
		total  int64
		ips    map[string]bool
		req    []int64
		bytes  []int64
	}
	poolStats := map[uint]*poolStat{}
	sumInto := func(dst, src []int64) {
		for i := 0; i < points && i < len(src); i++ {
			dst[i] += src[i]
		}
	}
	for _, site := range sites {
		ps := poolStats[site.ApplicationID]
		if ps == nil {
			ps = &poolStat{ips: map[string]bool{}, req: newSeries(), bytes: newSeries()}
			poolStats[site.ApplicationID] = ps
		}
		ps.active += siteActive[site.ID]
		ps.total += siteTotal[site.ID]
		sumInto(ps.req, siteReq[site.ID])
		sumInto(ps.bytes, siteBytes[site.ID])
		for ip := range siteIPs[site.ID] {
			ps.ips[ip] = true
		}
	}
	totalRequests := int64(0)
	for _, n := range siteTotal {
		totalRequests += n
	}
	nodes := []topoNode{
		{ID: "internet", Type: "internet", Label: "用户端", Data: map[string]any{
			"kind": "internet", "qps": math.Round(totalQPS*100) / 100,
			"active": status.ActiveConnections, "total_requests": totalRequests,
			"active_ips": len(allIPs), "requests_series": allReq, "bytes_series": allBytes,
		}},
		{ID: "proxy", Type: "proxy", Label: "代理引擎", Data: map[string]any{
			"kind": "proxy", "running": status.Running, "config_version": status.ConfigVersion,
			"connections": status.ActiveConnections, "qps": math.Round(totalQPS*100) / 100,
			"active": status.ActiveConnections, "total_requests": totalRequests,
			"active_ips": len(allIPs), "requests_series": allReq, "bytes_series": allBytes,
		}},
	}
	edges := []topoEdge{{ID: "e-internet-proxy", Source: "internet", Target: "proxy", Animated: true, QPS: math.Round(totalQPS*100) / 100}}

	for _, site := range sites {
		upHealthy := false
		if m, ok := live[site.ApplicationID]; ok {
			for _, h := range m {
				if h {
					upHealthy = true
					break
				}
			}
		}
		siteReqSeries := siteReq[site.ID]
		if siteReqSeries == nil {
			siteReqSeries = make([]int64, points)
		}
		siteByteSeries := siteBytes[site.ID]
		if siteByteSeries == nil {
			siteByteSeries = make([]int64, points)
		}
		nodes = append(nodes, topoNode{
			ID: fmt.Sprintf("site-%d", site.ID), Type: "site", Label: site.Name,
			Data: map[string]any{
				"kind": "site", "id": site.ID, "domains": site.Domains, "enabled": site.Enabled,
				"protocol": site.Protocol, "healthy": upHealthy, "port": site.ListenPort, "qps": siteQPS[site.ID],
				"active": siteActive[site.ID], "total_requests": siteTotal[site.ID],
				"active_ips": len(siteIPs[site.ID]), "requests_series": siteReqSeries, "bytes_series": siteByteSeries,
			},
		})
		edges = append(edges, topoEdge{ID: fmt.Sprintf("e-proxy-site-%d", site.ID), Source: "proxy", Target: fmt.Sprintf("site-%d", site.ID), Animated: site.Enabled, QPS: siteQPS[site.ID]})
		edges = append(edges, topoEdge{ID: fmt.Sprintf("e-site-up-%d", site.ID), Source: fmt.Sprintf("site-%d", site.ID), Target: fmt.Sprintf("application-%d", site.ApplicationID), Animated: site.Enabled && upHealthy, QPS: siteQPS[site.ID]})
	}
	for _, up := range ups {
		healthy := 0
		total := 0
		for _, n := range up.Nodes {
			total++
			if live[up.ID][n.ID] {
				healthy++
			}
		}
		ps := poolStats[up.ID]
		if ps == nil {
			ps = &poolStat{ips: map[string]bool{}, req: newSeries(), bytes: newSeries()}
		}
		nodes = append(nodes, topoNode{
			ID: fmt.Sprintf("application-%d", up.ID), Type: "application", Label: up.Name,
			Data: map[string]any{
				"kind": "application", "id": up.ID, "strategy": up.Strategy, "nodes": total, "healthy": healthy, "enabled": up.Enabled,
				"qps": math.Round(poolQPS[up.ID]*100) / 100, "active": ps.active, "total_requests": ps.total,
				"active_ips": len(ps.ips), "requests_series": ps.req, "bytes_series": ps.bytes,
			},
		})
		for _, n := range up.Nodes {
			reqSeries := nodeReq[n.Address]
			if reqSeries == nil {
				reqSeries = newSeries()
			}
			byteSeries := nodeBytes[n.Address]
			if byteSeries == nil {
				byteSeries = newSeries()
			}
			nodes = append(nodes, topoNode{
				ID: fmt.Sprintf("node-%d", n.ID), Type: "application_node", Label: n.Address,
				Data: map[string]any{
					"kind": "application_node", "id": n.ID, "address": n.Address,
					"healthy": live[up.ID][n.ID], "enabled": n.Enabled, "weight": n.Weight, "priority": n.Priority,
					"qps": nodeQPS[n.Address], "active": nodeActive[n.Address], "total_requests": nodeTotal[n.Address],
					"active_ips": len(nodeIPs[n.Address]), "requests_series": reqSeries, "bytes_series": byteSeries,
				},
			})
			edges = append(edges, topoEdge{
				ID: fmt.Sprintf("e-up-node-%d", n.ID), Source: fmt.Sprintf("application-%d", up.ID), Target: fmt.Sprintf("node-%d", n.ID),
				Animated: live[up.ID][n.ID], QPS: nodeQPS[n.Address],
			})
		}
	}
	response.OK(w, map[string]any{"nodes": nodes, "edges": edges, "generated_at": time.Now()})
}
