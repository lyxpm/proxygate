package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/proxygate/proxygate/internal/proxy/balancer"
)

var domainPattern = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

type snapshot struct {
	cfg          *Config
	ports        map[int]*portRuntime
	applications map[uint]*applicationRuntime
}

// closeIdleConnections releases connection pools held by a retired snapshot.
func (s *snapshot) closeIdleConnections() {
	if s == nil {
		return
	}
	for _, pr := range s.ports {
		for _, sr := range pr.index.all {
			if h, ok := sr.handler.(*siteHandler); ok {
				h.closeIdleConnections()
			}
		}
	}
}

type portRuntime struct {
	port      int
	tls       bool
	index     *siteIndex
	redirects *siteIndex
	certs     *certIndex
}

type siteRuntime struct {
	cfg     SiteConfig
	handler http.Handler
}

type applicationRuntime struct {
	cfg           ApplicationConfig
	nodes         []*balancer.Node
	byID          map[uint]*balancer.Node
	bal           balancer.Balancer
	health        HealthConfig
	obs           Observer
	healthClients map[uint]*http.Client

	mu    sync.Mutex
	fails map[uint]int
}

type siteIndex struct {
	exact map[string]*siteRuntime
	wild  map[string]*siteRuntime
	deflt *siteRuntime
	all   []*siteRuntime
}

func newSiteIndex() *siteIndex {
	return &siteIndex{exact: make(map[string]*siteRuntime), wild: make(map[string]*siteRuntime)}
}

func (i *siteIndex) add(s *siteRuntime) {
	i.all = append(i.all, s)
	for _, d := range s.cfg.Domains {
		d = normalizeDomain(d)
		switch {
		case d == "*":
			if i.deflt == nil {
				i.deflt = s
			}
		case strings.HasPrefix(d, "*."):
			root := strings.TrimPrefix(d, "*.")
			if _, ok := i.wild[root]; !ok {
				i.wild[root] = s
			}
		default:
			if _, ok := i.exact[d]; !ok {
				i.exact[d] = s
			}
		}
	}
}

func (i *siteIndex) match(host string) *siteRuntime {
	if i == nil {
		return nil
	}
	host = normalizeDomain(host)
	if s := i.exact[host]; s != nil {
		return s
	}
	labels := strings.Split(host, ".")
	for n := 1; n < len(labels); n++ {
		if s := i.wild[strings.Join(labels[n:], ".")]; s != nil {
			return s
		}
	}
	return i.deflt
}

type certIndex struct {
	exact map[string]*tls.Certificate
	wild  map[string]*tls.Certificate
	ips   map[string]*tls.Certificate
}

func newCertIndex() *certIndex {
	return &certIndex{exact: map[string]*tls.Certificate{}, wild: map[string]*tls.Certificate{}, ips: map[string]*tls.Certificate{}}
}

func (c *certIndex) add(cert *tls.Certificate) {
	if cert == nil {
		return
	}
	leaf := cert.Leaf
	if leaf == nil && len(cert.Certificate) > 0 {
		if parsed, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
			leaf = parsed
			cert.Leaf = parsed
		}
	}
	if leaf == nil {
		return
	}
	names := append([]string{}, leaf.DNSNames...)
	names = append(names, leaf.Subject.CommonName)
	for _, raw := range names {
		d := normalizeDomain(raw)
		if d == "" {
			continue
		}
		if strings.HasPrefix(d, "*.") {
			root := strings.TrimPrefix(d, "*.")
			if old := c.wild[root]; old == nil || later(leaf, old) {
				c.wild[root] = cert
			}
			continue
		}
		if old := c.exact[d]; old == nil || later(leaf, old) {
			c.exact[d] = cert
		}
	}
	for _, ip := range leaf.IPAddresses {
		c.ips[ip.String()] = cert
	}
}

func later(leaf *x509.Certificate, old *tls.Certificate) bool {
	if old.Leaf == nil {
		return true
	}
	return leaf.NotAfter.After(old.Leaf.NotAfter)
}

func (c *certIndex) get(serverName string) (*tls.Certificate, error) {
	name := normalizeDomain(serverName)
	if ip := net.ParseIP(name); ip != nil {
		if cert := c.ips[ip.String()]; cert != nil {
			return cert, nil
		}
	}
	if cert := c.exact[name]; cert != nil {
		return cert, nil
	}
	labels := strings.Split(name, ".")
	for n := 1; n < len(labels); n++ {
		if cert := c.wild[strings.Join(labels[n:], ".")]; cert != nil {
			return cert, nil
		}
	}
	return nil, fmt.Errorf("no certificate for %q", serverName)
}

func normalizeDomain(d string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
}

// buildSnapshot validates the config and prepares runtime handlers.
func (e *Engine) buildSnapshot(cfg *Config) (*snapshot, map[int]bool, error) {
	if len(cfg.Sites) == 0 && len(cfg.Applications) == 0 {
		// An empty config is valid: it stops all proxying.
	}
	var problems []string
	old := e.snap.Load()
	ups := make(map[uint]*applicationRuntime, len(cfg.Applications))
	for id, uc := range cfg.Applications {
		if id == 0 {
			problems = append(problems, "application id must not be zero")
			continue
		}
		if strings.TrimSpace(uc.Name) == "" {
			problems = append(problems, fmt.Sprintf("application %d has no name", id))
		}
		nodes := make([]*balancer.Node, 0, len(uc.Nodes))
		byID := make(map[uint]*balancer.Node, len(uc.Nodes))
		for _, nc := range uc.Nodes {
			if nc.ID == 0 {
				problems = append(problems, fmt.Sprintf("application %q has a node without id", uc.Name))
				continue
			}
			u, err := url.Parse(normalizeNodeAddress(nc.Address))
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				problems = append(problems, fmt.Sprintf("application %q node %d has invalid address %q", uc.Name, nc.ID, nc.Address))
				continue
			}
			node := balancer.NewNode(nc.ID, u, nc.Weight, nc.Priority)
			node.Enabled = nc.Enabled
			healthy := nc.Enabled && nc.Healthy
			if old != nil {
				if ou := old.applications[uc.ID]; ou != nil {
					if on := ou.byID[nc.ID]; on != nil {
						healthy = nc.Enabled && on.Healthy()
					}
				}
			}
			node.SetHealthy(healthy)
			nodes = append(nodes, node)
			byID[nc.ID] = node
		}
		ups[id] = &applicationRuntime{
			cfg: uc, nodes: nodes, byID: byID,
			bal:           balancer.New(uc.Strategy, nodes),
			health:        uc.Health,
			obs:           e.observer,
			healthClients: make(map[uint]*http.Client, len(nodes)),
		}
		for _, nc := range uc.Nodes {
			if client := newHealthClient(uc.Health.Timeout, nc); client != nil {
				if up := ups[id]; up != nil {
					up.healthClients[nc.ID] = client
				}
			}
		}
	}

	portSites := make(map[int][]*siteRuntime)
	portTLS := make(map[int]bool)
	for _, sc := range cfg.Sites {
		if !sc.Enabled {
			continue
		}
		if sc.ListenPort < 1 || sc.ListenPort > 65535 {
			problems = append(problems, fmt.Sprintf("site %q has invalid listen port %d", sc.Name, sc.ListenPort))
			continue
		}
		if sc.Protocol != "http" && sc.Protocol != "https" {
			problems = append(problems, fmt.Sprintf("site %q has invalid protocol %q", sc.Name, sc.Protocol))
			continue
		}
		up := ups[sc.ApplicationID]
		if up == nil {
			problems = append(problems, fmt.Sprintf("site %q references missing application %d", sc.Name, sc.ApplicationID))
			continue
		}
		if !up.cfg.Enabled {
			problems = append(problems, fmt.Sprintf("site %q references disabled application %q", sc.Name, up.cfg.Name))
			continue
		}
		usable := 0
		for _, n := range up.cfg.Nodes {
			if n.Enabled {
				usable++
			}
		}
		if usable == 0 {
			problems = append(problems, fmt.Sprintf("site %q application %q has no enabled nodes", sc.Name, up.cfg.Name))
			continue
		}
		domains := make([]string, 0, len(sc.Domains))
		for _, d := range sc.Domains {
			d = normalizeDomain(d)
			if !validDomain(d) {
				problems = append(problems, fmt.Sprintf("site %q has invalid domain %q", sc.Name, d))
				continue
			}
			domains = append(domains, d)
		}
		if len(domains) == 0 {
			problems = append(problems, fmt.Sprintf("site %q has no valid domains", sc.Name))
			continue
		}
		sc.Domains = domains
		if sc.Protocol == "https" && sc.TLS == nil {
			problems = append(problems, fmt.Sprintf("site %q uses HTTPS but has no certificate", sc.Name))
			continue
		}
		portSites[sc.ListenPort] = append(portSites[sc.ListenPort], &siteRuntime{
			cfg:     sc,
			handler: newSiteHandler(sc, up, e),
		})
		if sc.Protocol == "https" {
			portTLS[sc.ListenPort] = true
		}
	}

	for port, sites := range portSites {
		seen := map[string]string{}
		for _, s := range sites {
			for _, d := range s.cfg.Domains {
				if other, ok := seen[d]; ok {
					problems = append(problems, fmt.Sprintf("domain %q is used by both %q and %q on port %d", d, other, s.cfg.Name, port))
				} else {
					seen[d] = s.cfg.Name
				}
			}
		}
	}
	// A port must be either plaintext or TLS for all sites.
	for port := range portSites {
		hasTLS := portTLS[port]
		for _, s := range portSites[port] {
			if (s.cfg.Protocol == "https") != hasTLS {
				problems = append(problems, fmt.Sprintf("port %d mixes HTTP and HTTPS sites", port))
				break
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, nil, errors.New(strings.Join(problems, "; "))
	}

	snap := &snapshot{cfg: cfg, ports: make(map[int]*portRuntime), applications: ups}
	for port, sites := range portSites {
		pr := &portRuntime{port: port, tls: portTLS[port], index: newSiteIndex()}
		for _, s := range sites {
			pr.index.add(s)
		}
		if pr.tls {
			pr.certs = newCertIndex()
			for _, s := range sites {
				pr.certs.add(s.cfg.TLS)
			}
		}
		snap.ports[port] = pr
	}

	// HTTPS sites with redirect enabled also need a listener on :80.
	redirects := newSiteIndex()
	for _, s := range cfg.Sites {
		if !s.Enabled || s.Protocol != "https" || !s.RedirectHTTPS {
			continue
		}
		redirects.add(&siteRuntime{cfg: s})
	}
	if len(redirects.all) > 0 {
		pr := snap.ports[80]
		if pr == nil {
			pr = &portRuntime{port: 80, index: newSiteIndex()}
			snap.ports[80] = pr
		}
		if pr.tls {
			return nil, nil, errors.New("port 80 cannot be both a TLS listener and an HTTPS redirect listener")
		}
		pr.redirects = redirects
	}

	modes := make(map[int]bool, len(snap.ports))
	for port, pr := range snap.ports {
		modes[port] = pr.tls
	}
	return snap, modes, nil
}

func normalizeNodeAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return addr
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	return addr
}

func validDomain(d string) bool {
	if d == "*" {
		return true
	}
	if net.ParseIP(d) != nil {
		return true
	}
	if len(d) > 253 {
		return false
	}
	return domainPattern.MatchString(d)
}

// restartCheckers stops old probes and starts probes for the new snapshot.
func (e *Engine) restartCheckers(snap *snapshot) {
	for id, cancel := range e.checkers {
		cancel()
		delete(e.checkers, id)
	}
	ctx := e.rootCtx
	if ctx == nil {
		return
	}
	for _, up := range snap.applications {
		if !up.health.Enabled {
			continue
		}
		cctx, cancel := context.WithCancel(ctx)
		e.checkers[up.cfg.ID] = cancel
		for _, node := range up.nodes {
			cfg, ok := nodeConfigByID(up.cfg, node.ID)
			if !ok || !cfg.Enabled {
				continue
			}
			go e.checkLoop(cctx, up, node, cfg)
		}
	}
}

func nodeConfigByID(up ApplicationConfig, id uint) (NodeConfig, bool) {
	for _, n := range up.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return NodeConfig{}, false
}
