package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"
)

// Options configures an Engine.
type Options struct {
	Observer       Observer
	Logger         *slog.Logger
	TrustedProxies []string
}

// Engine owns the listening sockets and the atomic runtime snapshot.
type Engine struct {
	mu      sync.Mutex
	servers map[int]*portServer
	snap    atomic.Pointer[snapshot]

	observer   Observer
	log        *slog.Logger
	trusted    []*net.IPNet
	challenges *ChallengeStore

	active    atomic.Int64
	running   atomic.Bool
	startedAt time.Time
	lastApply atomic.Int64
	lastErr   atomic.Value // string

	rootCtx  context.Context
	cancel   context.CancelFunc
	checkers map[uint]context.CancelFunc
	h3Mu     sync.RWMutex
	h3Up     map[int]bool
}

type portServer struct {
	port int
	tls  bool
	srv  *http.Server
	ln   net.Listener
	h3   *http3.Server
	udp  net.PacketConn
}

// New creates an engine. Start must be called before Apply.
func New(opts Options) *Engine {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	e := &Engine{
		servers:    make(map[int]*portServer),
		observer:   opts.Observer,
		log:        logger,
		challenges: NewChallengeStore(),
		checkers:   make(map[uint]context.CancelFunc),
		h3Up:       make(map[int]bool),
	}
	for _, raw := range opts.TrustedProxies {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			if ip := net.ParseIP(raw); ip != nil {
				if ip.To4() != nil {
					raw += "/32"
				} else {
					raw += "/128"
				}
			}
		}
		if _, n, err := net.ParseCIDR(raw); err == nil {
			e.trusted = append(e.trusted, n)
		} else {
			logger.Warn("ignoring invalid trusted proxy", "value", raw, "error", err)
		}
	}
	return e
}

// Challenges exposes the ACME HTTP-01 token store to the certificate manager.
func (e *Engine) Challenges() *ChallengeStore {
	e.challenges.portListening = e.PortListening
	return e.challenges
}

// Start initializes the engine lifetime; listeners are created by Apply.
func (e *Engine) Start(parent context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running.Load() {
		return errors.New("proxy engine already started")
	}
	e.rootCtx, e.cancel = context.WithCancel(parent)
	e.startedAt = time.Now()
	e.running.Store(true)
	return nil
}

// Apply validates cfg, builds the runtime and atomically swaps routing. A
// failure leaves the previous configuration serving.
func (e *Engine) Apply(cfg *Config) error {
	if !e.running.Load() {
		return errors.New("proxy engine is not started")
	}
	if cfg == nil {
		return errors.New("nil runtime config")
	}
	snap, ports, err := e.buildSnapshot(cfg)
	if err != nil {
		e.setLastError(err)
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	// Bind new sockets first so a failure never disturbs running listeners.
	created := make(map[int]*portServer)
	for port, tlsMode := range ports {
		if old, ok := e.servers[port]; ok {
			if old.tls == tlsMode {
				continue
			}
			// Mode change requires a fresh socket.
			if err := shutdownServer(old, 2*time.Second); err != nil {
				e.log.Warn("close listener for mode change", "port", port, "error", err)
			}
			delete(e.servers, port)
		}
		ps, err := e.newPortServer(port, tlsMode)
		if err != nil {
			for _, p := range created {
				_ = shutdownServer(p, time.Second)
			}
			e.setLastError(err)
			return fmt.Errorf("listen on :%d: %w", port, err)
		}
		created[port] = ps
		e.servers[port] = ps
	}

	// Stop listeners that are no longer configured.
	for port, ps := range e.servers {
		if _, keep := ports[port]; keep {
			continue
		}
		delete(e.servers, port)
		go func(ps *portServer) {
			if err := shutdownServer(ps, 5*time.Second); err != nil {
				e.log.Warn("shutdown removed listener", "port", ps.port, "error", err)
			}
		}(ps)
	}

	old := e.snap.Swap(snap)
	e.lastApply.Store(time.Now().UnixNano())
	e.lastErr.Store("")
	if old != nil {
		old.closeIdleConnections()
	}
	e.restartCheckers(snap)
	siteCount := 0
	for _, pr := range snap.ports {
		siteCount += len(pr.index.all)
	}
	e.log.Info("proxy configuration applied", "version", cfg.Version, "sites", siteCount, "ports", len(ports))
	return nil
}

// Stop shuts down all listeners and health checkers.
func (e *Engine) Stop(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running.Load() {
		return nil
	}
	e.running.Store(false)
	if e.cancel != nil {
		e.cancel()
	}
	for id, cancel := range e.checkers {
		cancel()
		delete(e.checkers, id)
	}
	var firstErr error
	for port, ps := range e.servers {
		if err := shutdownServer(ps, 5*time.Second); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(e.servers, port)
	}
	if old := e.snap.Swap(nil); old != nil {
		old.closeIdleConnections()
	}
	e.log.Info("proxy engine stopped")
	return firstErr
}

func shutdownServer(ps *portServer, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if ps.h3 != nil {
		_ = ps.h3.Close()
	}
	if ps.udp != nil {
		_ = ps.udp.Close()
	}
	if err := ps.srv.Shutdown(ctx); err != nil {
		_ = ps.srv.Close()
		return err
	}
	return nil
}

func (e *Engine) newPortServer(port int, tlsMode bool) (*portServer, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, err
	}
	ps := &portServer{port: port, tls: tlsMode, ln: ln}
	ps.srv = &http.Server{
		Handler:           e,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(e.log.Handler(), slog.LevelWarn),
	}
	var tlsCfg *tls.Config
	if tlsMode {
		tlsCfg = &tls.Config{
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"h2", "http/1.1"},
			GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				return e.certificateFor(port, hello.ServerName)
			},
		}
		ps.srv.TLSConfig = tlsCfg
		if err := http2.ConfigureServer(ps.srv, &http2.Server{}); err != nil {
			ln.Close()
			return nil, fmt.Errorf("configure HTTP/2: %w", err)
		}
		e.startHTTP3(ps, tlsCfg)
	}
	go func() {
		var serveErr error
		if tlsMode {
			serveErr = ps.srv.Serve(tls.NewListener(ln, tlsCfg))
		} else {
			serveErr = ps.srv.Serve(ln)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			e.log.Error("proxy listener stopped", "port", port, "error", serveErr)
		}
	}()
	e.log.Info("proxy listener started", "port", port, "tls", tlsMode)
	return ps, nil
}

func (e *Engine) startHTTP3(ps *portServer, certCfg *tls.Config) {
	udp, err := net.ListenPacket("udp", fmt.Sprintf(":%d", ps.port))
	if err != nil {
		e.log.Warn("HTTP/3 listener unavailable", "port", ps.port, "error", err)
		e.h3Mu.Lock()
		e.h3Up[ps.port] = false
		e.h3Mu.Unlock()
		return
	}
	h3tls := certCfg.Clone()
	h3tls.NextProtos = []string{"h3"}
	srv := &http3.Server{Handler: e, TLSConfig: h3tls}
	ps.h3 = srv
	ps.udp = udp
	e.h3Mu.Lock()
	e.h3Up[ps.port] = true
	e.h3Mu.Unlock()
	go func() {
		if err := srv.Serve(udp); err != nil && !errors.Is(err, http.ErrServerClosed) {
			e.log.Warn("HTTP/3 listener stopped", "port", ps.port, "error", err)
		}
	}()
	e.log.Info("HTTP/3 listener started", "port", ps.port)
}

func (e *Engine) certificateFor(port int, serverName string) (*tls.Certificate, error) {
	snap := e.snap.Load()
	if snap == nil {
		return nil, errors.New("no runtime configuration")
	}
	pr := snap.ports[port]
	if pr == nil || pr.certs == nil {
		return nil, fmt.Errorf("no TLS configuration for port %d", port)
	}
	return pr.certs.get(serverName)
}

func (e *Engine) setLastError(err error) {
	if err == nil {
		e.lastErr.Store("")
		return
	}
	e.lastErr.Store(err.Error())
}

// ServeHTTP routes by local port and Host against the current snapshot.
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	snap := e.snap.Load()
	if snap == nil || !e.running.Load() {
		http.Error(w, "proxy is not ready", http.StatusServiceUnavailable)
		return
	}
	e.active.Add(1)
	defer e.active.Add(-1)

	r = e.prepareRequest(r)

	if strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
		if e.challenges.Serve(w, r) {
			return
		}
	}

	port := localPort(r)
	pr := snap.ports[port]
	if pr == nil {
		http.NotFound(w, r)
		return
	}
	if pr.tls && r.ProtoMajor < 3 {
		e.h3Mu.RLock()
		up := e.h3Up[port]
		e.h3Mu.RUnlock()
		if up {
			w.Header().Set("Alt-Svc", fmt.Sprintf(`h3=":%d"; ma=86400`, port))
		}
	}
	host := hostOnly(r.Host)
	if site := pr.index.match(host); site != nil {
		site.handler.ServeHTTP(w, r)
		return
	}
	if pr.redirects != nil {
		if target := pr.redirects.match(host); target != nil {
			redirectToHTTPS(w, r, target.cfg.ListenPort)
			return
		}
	}
	http.NotFound(w, r)
}

// Status returns a runtime snapshot for the API.
func (e *Engine) Status() Status {
	st := Status{
		Running:           e.running.Load(),
		StartedAt:         e.startedAt,
		ActiveConnections: e.active.Load(),
	}
	if v := e.lastApply.Load(); v > 0 {
		st.LastApply = time.Unix(0, v)
	}
	if v, ok := e.lastErr.Load().(string); ok {
		st.LastError = v
	}
	snap := e.snap.Load()
	if snap == nil {
		return st
	}
	st.ConfigVersion = snap.cfg.Version
	ports := make([]int, 0, len(snap.ports))
	for p := range snap.ports {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	for _, p := range ports {
		pr := snap.ports[p]
		st.Ports = append(st.Ports, PortStatus{Port: p, TLS: pr.tls, Sites: len(pr.index.all)})
	}
	ups := make([]ApplicationView, 0, len(snap.applications))
	for _, up := range snap.applications {
		v := ApplicationView{ID: up.cfg.ID, Name: up.cfg.Name, Strategy: up.cfg.Strategy}
		for _, n := range up.nodes {
			v.Nodes = append(v.Nodes, NodeStatus{
				ID: n.ID, Address: n.URL.String(), Healthy: n.Healthy(),
				Active: n.Conns(), Requests: n.Requests(),
			})
		}
		ups = append(ups, v)
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].ID < ups[j].ID })
	st.Applications = ups
	seen := make(map[uint]bool)
	for _, pr := range snap.ports {
		for _, sr := range pr.index.all {
			if seen[sr.cfg.ID] {
				continue
			}
			seen[sr.cfg.ID] = true
			active := int64(0)
			if sh, ok := sr.handler.(*siteHandler); ok {
				active = sh.active.Load()
			}
			st.Sites = append(st.Sites, SiteStatus{ID: sr.cfg.ID, Active: active})
		}
	}
	sort.Slice(st.Sites, func(i, j int) bool { return st.Sites[i].ID < st.Sites[j].ID })
	return st
}

func localPort(r *http.Request) int {
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if _, port, err := net.SplitHostPort(a.String()); err == nil {
			if n, err := net.LookupPort("tcp", port); err == nil {
				return n
			}
		}
	}
	if r.TLS != nil {
		return 443
	}
	return 80
}

func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func redirectToHTTPS(w http.ResponseWriter, r *http.Request, port int) {
	target := "https://" + hostOnly(r.Host)
	if port != 443 {
		target += fmt.Sprintf(":%d", port)
	}
	target += r.URL.RequestURI()
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// PortListening reports whether the engine currently owns a listener on port.
func (e *Engine) PortListening(port int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.servers[port]
	return ok
}
