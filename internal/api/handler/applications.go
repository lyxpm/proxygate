package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/application"
	"github.com/proxygate/proxygate/internal/proxy"
	"github.com/proxygate/proxygate/internal/storage"
)

type nodeView struct {
	ID            uint      `json:"id"`
	ApplicationID uint      `json:"application_id"`
	Address       string    `json:"address"`
	Weight        int       `json:"weight"`
	Priority      int       `json:"priority"`
	Enabled       bool      `json:"enabled"`
	TLSVerify     bool      `json:"tls_verify"`
	Healthy       bool      `json:"healthy"`
	ActiveConns   int64     `json:"active_connections"`
	TotalRequests int64     `json:"total_requests"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type applicationView struct {
	ID                  uint       `json:"id"`
	Name                string     `json:"name"`
	Strategy            string     `json:"strategy"`
	Enabled             bool       `json:"enabled"`
	HealthCheckEnabled  bool       `json:"health_check_enabled"`
	HealthCheckType     string     `json:"health_check_type"`
	HealthCheckPath     string     `json:"health_check_path"`
	HealthCheckInterval int        `json:"health_check_interval"`
	HealthCheckTimeout  int        `json:"health_check_timeout"`
	HealthyThreshold    int        `json:"healthy_threshold"`
	UnhealthyThreshold  int        `json:"unhealthy_threshold"`
	PassiveFailureLimit int        `json:"passive_failure_limit"`
	PassiveCooldown     int        `json:"passive_cooldown"`
	Nodes               []nodeView `json:"nodes"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

func (s *Server) toApplicationView(up storage.Application) applicationView {
	status := s.engineApplications()
	live := map[uint]proxy.NodeStatus{}
	if st, ok := status[up.ID]; ok {
		for _, n := range st.Nodes {
			live[n.ID] = n
		}
	}
	v := applicationView{
		ID: up.ID, Name: up.Name, Strategy: up.Strategy, Enabled: up.Enabled,
		HealthCheckEnabled: up.HealthCheckEnabled, HealthCheckType: up.HealthCheckType,
		HealthCheckPath: up.HealthCheckPath, HealthCheckInterval: up.HealthCheckInterval,
		HealthCheckTimeout: up.HealthCheckTimeout, HealthyThreshold: up.HealthyThreshold,
		UnhealthyThreshold: up.UnhealthyThreshold, PassiveFailureLimit: up.PassiveFailureLimit,
		PassiveCooldown: up.PassiveCooldown, CreatedAt: up.CreatedAt, UpdatedAt: up.UpdatedAt,
	}
	for _, n := range up.Nodes {
		nv := nodeView{
			ID: n.ID, ApplicationID: n.ApplicationID, Address: n.Address, Weight: n.Weight,
			Priority: n.Priority, Enabled: n.Enabled, TLSVerify: n.TLSVerify, Healthy: n.Healthy,
			CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
		}
		if l, ok := live[n.ID]; ok {
			nv.Healthy = l.Healthy
			nv.ActiveConns = l.Active
			nv.TotalRequests = l.Requests
		}
		v.Nodes = append(v.Nodes, nv)
	}
	return v
}

func (s *Server) engineApplications() map[uint]proxy.ApplicationView {
	out := make(map[uint]proxy.ApplicationView)
	if s.Engine == nil {
		return out
	}
	for _, up := range s.Engine.Status().Applications {
		out[up.ID] = up
	}
	return out
}

// ListApplications returns all groups.
func (s *Server) ListApplications(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Applications.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	items := make([]applicationView, 0, len(rows))
	for _, row := range rows {
		if q != "" && !strings.Contains(strings.ToLower(row.Name), q) {
			continue
		}
		items = append(items, s.toApplicationView(row))
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

// CreateApplication creates a group.
func (s *Server) CreateApplication(w http.ResponseWriter, r *http.Request) {
	var in application.Input
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	row, err := s.Applications.Create(r.Context(), in)
	if err != nil {
		s.audit(r, "create_application", "application", nil, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "create_application", "application", row.ID, "success", map[string]any{"name": row.Name})
	response.Created(w, s.toApplicationView(*row))
}

// GetApplication returns one group.
func (s *Server) GetApplication(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	row, err := s.Applications.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, s.toApplicationView(*row))
}

// UpdateApplication saves a group.
func (s *Server) UpdateApplication(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in application.Input
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	row, err := s.Applications.Update(r.Context(), id, in)
	if err != nil {
		s.audit(r, "update_application", "application", id, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "update_application", "application", id, "success", nil)
	response.OK(w, s.toApplicationView(*row))
}

// DeleteApplication removes a group.
func (s *Server) DeleteApplication(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Applications.Delete(r.Context(), id); err != nil {
		s.audit(r, "delete_application", "application", id, "failure", nil)
		s.fail(w, r, err)
		return
	}
	s.audit(r, "delete_application", "application", id, "success", nil)
	w.WriteHeader(http.StatusNoContent)
}

// AddNode appends a backend.
func (s *Server) AddNode(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in application.NodeInput
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	node, err := s.Applications.AddNode(r.Context(), id, in)
	if err != nil {
		s.audit(r, "add_application_node", "application", id, "failure", nil)
		s.fail(w, r, err)
		return
	}
	up, _ := s.Applications.Get(r.Context(), id)
	s.audit(r, "add_application_node", "application", id, "success", map[string]any{"address": node.Address})
	if up != nil {
		response.Created(w, s.toApplicationView(*up))
		return
	}
	response.OK(w, node)
}

// UpdateNode saves a backend.
func (s *Server) UpdateNode(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	nodeID, err := parseID(r, "node_id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in application.NodeInput
	if err := response.Decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.Applications.UpdateNode(r.Context(), id, nodeID, in); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "update_application_node", "application", id, "success", nil)
	up, err := s.Applications.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, s.toApplicationView(*up))
}

// DeleteNode removes a backend.
func (s *Server) DeleteNode(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	nodeID, err := parseID(r, "node_id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Applications.DeleteNode(r.Context(), id, nodeID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "delete_application_node", "application", id, "success", nil)
	up, err := s.Applications.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, s.toApplicationView(*up))
}

// ApplicationHealth returns live node health.
func (s *Server) ApplicationHealth(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	up, err := s.Applications.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	live := s.engineApplications()
	response.OK(w, map[string]any{"application": s.toApplicationView(*up), "live": live[id]})
}
