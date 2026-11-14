// Package application manages load-balancing groups and their backend nodes.
package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/apperr"
	"github.com/proxygate/proxygate/internal/storage"
)

// Service implements application business rules on top of storage.
type Service struct {
	store         *storage.Store
	reload        func(context.Context) error
	log           *slog.Logger
	allowLoopback bool
}

// NewService creates an application service. reload is called after every
// successful mutation so the proxy runtime picks up the change.
func NewService(store *storage.Store, reload func(context.Context) error, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: store, reload: reload, log: log}
}

// SetAllowLoopback permits loopback upstreams for local sidecar deployments.
// It is disabled by default so user-provided node addresses cannot become an
// SSRF path to the management API or other local services.
func (s *Service) SetAllowLoopback(allow bool) { s.allowLoopback = allow }

// Input is the create/update payload for a group.
type Input struct {
	Name                string      `json:"name"`
	Strategy            string      `json:"strategy"`
	Enabled             *bool       `json:"enabled"`
	HealthCheckEnabled  *bool       `json:"health_check_enabled"`
	HealthCheckType     string      `json:"health_check_type"`
	HealthCheckPath     string      `json:"health_check_path"`
	HealthCheckInterval int         `json:"health_check_interval"`
	HealthCheckTimeout  int         `json:"health_check_timeout"`
	HealthyThreshold    int         `json:"healthy_threshold"`
	UnhealthyThreshold  int         `json:"unhealthy_threshold"`
	PassiveFailureLimit int         `json:"passive_failure_limit"`
	PassiveCooldown     int         `json:"passive_cooldown"`
	Nodes               []NodeInput `json:"nodes"`
}

// NodeInput is the create/update payload for one backend.
type NodeInput struct {
	ID        uint   `json:"id,omitempty"`
	Address   string `json:"address"`
	Weight    int    `json:"weight"`
	Priority  int    `json:"priority"`
	Enabled   *bool  `json:"enabled"`
	TLSVerify *bool  `json:"tls_verify"`
}

// List returns all groups.
func (s *Service) List(ctx context.Context) ([]storage.Application, error) {
	return s.store.ListApplications(ctx)
}

// Get returns one group.
func (s *Service) Get(ctx context.Context, id uint) (*storage.Application, error) {
	up, err := s.store.GetApplication(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, apperr.NotFoundf("application %d not found", id)
	}
	return up, err
}

// GetByCode returns one application by its immutable business code.
func (s *Service) GetByCode(ctx context.Context, code string) (*storage.Application, error) {
	up, err := s.store.GetApplicationByCode(ctx, code)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, apperr.NotFoundf("application %s not found", code)
	}
	return up, err
}

// Create validates and inserts a group with its nodes.
func (s *Service) Create(ctx context.Context, in Input) (*storage.Application, error) {
	up, err := s.build(ctx, 0, in)
	if err != nil {
		return nil, err
	}
	if len(up.Nodes) == 0 {
		return nil, apperr.InvalidField("nodes", "at least one application node is required")
	}
	if err := s.store.CreateApplication(ctx, up); err != nil {
		return nil, mapDBError(err)
	}
	if err := s.reload(ctx); err != nil {
		_ = s.store.DeleteApplication(ctx, up.ID)
		_ = s.reload(ctx)
		return nil, apperr.Internalf(err, "application was not applied")
	}
	return up, nil
}

// Update validates and saves group fields together with the full node list.
// Existing node IDs are preserved; omitted nodes are deleted atomically.
func (s *Service) Update(ctx context.Context, id uint, in Input) (*storage.Application, error) {
	up, err := s.store.GetApplication(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, apperr.NotFoundf("application %d not found", id)
	}
	if err != nil {
		return nil, err
	}
	if len(in.Nodes) == 0 {
		return nil, apperr.InvalidField("nodes", "at least one application node is required")
	}
	groupInput := in
	groupInput.Nodes = nil
	updated, err := s.build(ctx, id, groupInput)
	if err != nil {
		return nil, err
	}
	updated.Code = up.Code
	updated.CreatedAt = up.CreatedAt
	existing := make(map[uint]storage.ApplicationNode, len(up.Nodes))
	for _, n := range up.Nodes {
		existing[n.ID] = n
	}
	seenIDs := make(map[uint]bool, len(in.Nodes))
	seenAddresses := make(map[string]bool, len(in.Nodes))
	for _, item := range in.Nodes {
		node, err := s.buildNode(item)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(node.Address)
		if seenAddresses[key] {
			return nil, apperr.InvalidField("nodes", fmt.Sprintf("duplicate node address %s", node.Address))
		}
		seenAddresses[key] = true
		if item.ID > 0 {
			prev, ok := existing[item.ID]
			if !ok {
				return nil, apperr.NotFoundf("node %d not found", item.ID)
			}
			if seenIDs[item.ID] {
				return nil, apperr.InvalidField("nodes", "duplicate node id")
			}
			seenIDs[item.ID] = true
			node.ID = prev.ID
			node.ApplicationID = id
			node.CreatedAt = prev.CreatedAt
			node.Healthy = prev.Healthy
			if !strings.EqualFold(prev.Address, node.Address) {
				node.Healthy = true
			}
		} else {
			node.ApplicationID = id
		}
		updated.Nodes = append(updated.Nodes, *node)
	}
	updated.ID = up.ID
	updated.CreatedAt = up.CreatedAt
	if err := s.store.UpdateApplicationWithNodes(ctx, updated); err != nil {
		return nil, mapDBError(err)
	}
	if err := s.reload(ctx); err != nil {
		// Roll the persisted group and node list back to the last applied state.
		if restoreErr := s.store.UpdateApplicationWithNodes(ctx, up); restoreErr != nil {
			s.log.Error("rollback application after reload failure", "application", id, "error", restoreErr)
		}
		_ = s.reload(ctx)
		return nil, apperr.Internalf(err, "application saved but runtime update failed")
	}
	return s.store.GetApplication(ctx, id)
}

// Delete removes a group unless a site references it.
func (s *Service) Delete(ctx context.Context, id uint) error {
	up, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteApplication(ctx, id); err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return apperr.Conflictf("application is used by one or more sites")
		}
		return err
	}
	if err := s.reload(ctx); err != nil {
		if restoreErr := s.store.CreateApplication(ctx, up); restoreErr != nil {
			return apperr.Internalf(err, "application deleted but runtime update and restore failed: %s", restoreErr)
		}
		_ = s.reload(ctx)
		return apperr.Internalf(err, "application deleted but runtime update failed")
	}
	return nil
}

// AddNode appends a backend node.
func (s *Service) AddNode(ctx context.Context, applicationID uint, in NodeInput) (*storage.ApplicationNode, error) {
	up, err := s.Get(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	n, err := s.buildNode(in)
	if err != nil {
		return nil, err
	}
	for _, existing := range up.Nodes {
		if strings.EqualFold(existing.Address, n.Address) {
			return nil, apperr.Conflictf("node %s already exists in this application", n.Address)
		}
	}
	n.ApplicationID = applicationID
	if err := s.store.CreateNode(ctx, n); err != nil {
		return nil, mapDBError(err)
	}
	if err := s.reload(ctx); err != nil {
		return nil, apperr.Internalf(err, "node saved but runtime update failed")
	}
	return n, nil
}

// UpdateNode saves a backend node.
func (s *Service) UpdateNode(ctx context.Context, applicationID, nodeID uint, in NodeInput) (*storage.ApplicationNode, error) {
	if _, err := s.Get(ctx, applicationID); err != nil {
		return nil, err
	}
	n, err := s.store.GetNode(ctx, applicationID, nodeID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, apperr.NotFoundf("node %d not found", nodeID)
	}
	if err != nil {
		return nil, err
	}
	built, err := s.buildNode(in)
	if err != nil {
		return nil, err
	}
	built.ID = n.ID
	built.ApplicationID = applicationID
	built.CreatedAt = n.CreatedAt
	built.Healthy = n.Healthy
	if err := s.store.UpdateNode(ctx, built); err != nil {
		return nil, mapDBError(err)
	}
	if err := s.reload(ctx); err != nil {
		return nil, apperr.Internalf(err, "node saved but runtime update failed")
	}
	return built, nil
}

// DeleteNode removes a backend node.
func (s *Service) DeleteNode(ctx context.Context, applicationID, nodeID uint) error {
	if _, err := s.Get(ctx, applicationID); err != nil {
		return err
	}
	if err := s.store.DeleteNode(ctx, applicationID, nodeID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return apperr.NotFoundf("node %d not found", nodeID)
		}
		return err
	}
	if err := s.reload(ctx); err != nil {
		return apperr.Internalf(err, "node deleted but runtime update failed")
	}
	return nil
}

func (s *Service) build(ctx context.Context, id uint, in Input) (*storage.Application, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperr.InvalidField("name", "name is required")
	}
	if len(name) > 128 {
		return nil, apperr.InvalidField("name", "name is too long")
	}
	strategy := in.Strategy
	switch strategy {
	case "", "round_robin", "weighted_round_robin", "failover", "ip_hash", "least_connections":
		if strategy == "" {
			strategy = "round_robin"
		}
	default:
		return nil, apperr.InvalidField("strategy", "strategy must be round_robin, weighted_round_robin, failover, ip_hash or least_connections")
	}
	hcType := in.HealthCheckType
	if hcType == "" {
		hcType = "http"
	}
	if hcType != "http" && hcType != "tcp" {
		return nil, apperr.InvalidField("health_check_type", "health check type must be http or tcp")
	}
	path := strings.TrimSpace(in.HealthCheckPath)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return nil, apperr.InvalidField("health_check_path", "health check path must start with /")
	}
	interval := in.HealthCheckInterval
	if interval == 0 {
		interval = 10
	}
	timeout := in.HealthCheckTimeout
	if timeout == 0 {
		timeout = 3
	}
	if interval < 1 || interval > 3600 {
		return nil, apperr.InvalidField("health_check_interval", "interval must be between 1 and 3600 seconds")
	}
	if timeout < 1 || timeout > 60 || timeout >= interval {
		return nil, apperr.InvalidField("health_check_timeout", "timeout must be between 1 and 60 seconds and shorter than the interval")
	}
	healthy := in.HealthyThreshold
	if healthy == 0 {
		healthy = 2
	}
	unhealthy := in.UnhealthyThreshold
	if unhealthy == 0 {
		unhealthy = 3
	}
	if healthy < 1 || healthy > 10 || unhealthy < 1 || unhealthy > 20 {
		return nil, apperr.InvalidField("healthy_threshold", "thresholds are out of range")
	}
	passive := in.PassiveFailureLimit
	if passive == 0 {
		passive = 3
	}
	cool := in.PassiveCooldown
	if cool == 0 {
		cool = 30
	}
	if passive < 1 || passive > 20 {
		return nil, apperr.InvalidField("passive_failure_limit", "passive failure limit must be between 1 and 20")
	}
	if cool < 5 || cool > 3600 {
		return nil, apperr.InvalidField("passive_cooldown", "passive cooldown must be between 5 and 3600 seconds")
	}
	up := &storage.Application{
		ID: id, Name: name, Strategy: strategy, Enabled: boolOr(in.Enabled, true),
		HealthCheckEnabled: boolOr(in.HealthCheckEnabled, true), HealthCheckType: hcType,
		HealthCheckPath: path, HealthCheckInterval: interval, HealthCheckTimeout: timeout,
		HealthyThreshold: healthy, UnhealthyThreshold: unhealthy,
		PassiveFailureLimit: passive, PassiveCooldown: cool,
	}
	for _, n := range in.Nodes {
		node, err := s.buildNode(n)
		if err != nil {
			return nil, err
		}
		for _, prev := range up.Nodes {
			if strings.EqualFold(prev.Address, node.Address) {
				return nil, apperr.InvalidField("nodes", fmt.Sprintf("duplicate node address %s", node.Address))
			}
		}
		up.Nodes = append(up.Nodes, *node)
	}
	_ = ctx
	return up, nil
}

func (s *Service) buildNode(in NodeInput) (*storage.ApplicationNode, error) {
	addr, err := s.normalizeAddress(in.Address)
	if err != nil {
		return nil, err
	}
	weight := in.Weight
	if weight == 0 {
		weight = 1
	}
	if weight < 1 || weight > 1000 {
		return nil, apperr.InvalidField("weight", "weight must be between 1 and 1000")
	}
	if in.Priority < 0 || in.Priority > 100 {
		return nil, apperr.InvalidField("priority", "priority must be between 0 and 100")
	}
	return &storage.ApplicationNode{
		Address: addr, Weight: weight, Priority: in.Priority,
		Enabled: boolOr(in.Enabled, true), Healthy: true,
		TLSVerify: boolOr(in.TLSVerify, true),
	}, nil
}

// normalizeAddress canonicalises http(s)://host[:port][/path]; private
// upstreams are intentionally allowed, while link-local/metadata literals are
// rejected. Dial-time resolution is checked again in the proxy package.
func (s *Service) normalizeAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", apperr.InvalidField("address", "address is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", apperr.InvalidField("address", "invalid address")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", apperr.InvalidField("address", "scheme must be http or https")
	}
	if u.User != nil {
		return "", apperr.InvalidField("address", "credentials in application URLs are not allowed")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", apperr.InvalidField("address", "query strings and fragments are not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return "", apperr.InvalidField("address", "host is required")
	}
	lowerHost := strings.TrimSuffix(strings.ToLower(host), ".")
	if !s.allowLoopback && (lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".localhost")) {
		return "", apperr.InvalidField("address", "loopback addresses are not allowed")
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if ip.IsLoopback() && !s.allowLoopback {
			return "", apperr.InvalidField("address", "loopback addresses are not allowed")
		}
		if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return "", apperr.InvalidField("address", "link-local and metadata addresses are not allowed")
		}
	}
	if u.Path == "" {
		u.Path = ""
	}
	return u.String(), nil
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
		return apperr.Conflictf("a record with the same unique value already exists")
	}
	if strings.Contains(msg, "foreign key") {
		return apperr.Conflictf("record is still referenced by other resources")
	}
	return err
}

// NodeURL parses a stored address for callers that need a URL.
func NodeURL(addr string) (*url.URL, error) {
	return url.Parse(addr)
}

// NextRenewDelay is a helper for tests and documentation.
func NextRenewDelay(now time.Time) time.Duration {
	return 24*time.Hour - now.Sub(now.Truncate(24*time.Hour))
}
