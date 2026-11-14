package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/proxygate/proxygate/internal/proxy/balancer"
)

type nodeCtxKey struct{}
type clientIPCtxKey struct{}
type requestIDCtxKey struct{}

type siteHandler struct {
	site       SiteConfig
	up         *applicationRuntime
	proxy      *httputil.ReverseProxy
	transports map[uint]*http.Transport
	observer   Observer
	ipMode     string
	ipRules    []*net.IPNet
	active     atomic.Int64
	limiter    *siteLimiter
}

func newSiteHandler(site SiteConfig, up *applicationRuntime, e *Engine) http.Handler {
	h := &siteHandler{site: site, up: up, transports: make(map[uint]*http.Transport), observer: e.observer, ipMode: site.Options.IPMode, limiter: newSiteLimiter(site.Options)}
	for _, raw := range site.Options.IPRules {
		if _, network, err := net.ParseCIDR(raw); err == nil {
			h.ipRules = append(h.ipRules, network)
		}
	}
	for _, n := range up.nodes {
		if cfg, ok := nodeConfigByID(up.cfg, n.ID); ok {
			h.transports[n.ID] = newTransport(site, n, cfg)
		}
	}
	h.proxy = &httputil.ReverseProxy{
		Transport:     nodeTransport{handlers: h},
		FlushInterval: -1,
		BufferPool:    newBufferPool(site.Options.BufferSize),
		Rewrite: func(pr *httputil.ProxyRequest) {
			node, _ := pr.In.Context().Value(nodeCtxKey{}).(*balancer.Node)
			if node == nil {
				return
			}
			pr.SetURL(node.URL)
			if !site.Options.UseUpstreamHost {
				pr.Out.Host = pr.In.Host
			}
			rewritePath(pr, site.Options.StripPrefix, site.Options.AddPrefix)
			// Never let a client forge proxy headers.
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Real-IP")
			pr.SetXForwarded()
			if !site.Options.ForwardClientIP {
				pr.Out.Header.Del("X-Forwarded-For")
			}
			if port := requestLocalPort(pr.In); port != "" {
				pr.Out.Header.Set("X-Forwarded-Port", port)
			}
			applyHeaders(pr.Out.Header, site.Options.RequestHeaders)
		},
		ModifyResponse: func(resp *http.Response) error {
			applyHeaders(resp.Header, site.Options.ResponseHeaders)
			node, _ := resp.Request.Context().Value(nodeCtxKey{}).(*balancer.Node)
			if node != nil {
				if resp.StatusCode >= 500 {
					up.noteFailure(node, fmt.Sprintf("application returned HTTP %d", resp.StatusCode))
				} else {
					up.noteSuccess(node)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			node, _ := r.Context().Value(nodeCtxKey{}).(*balancer.Node)
			if r.Context().Err() != nil {
				writeStatus(w, 499)
				return
			}
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			if node != nil {
				up.noteFailure(node, err.Error())
			}
			http.Error(w, "bad gateway: application unavailable", http.StatusBadGateway)
		},
	}
	return h
}

func (h *siteHandler) closeIdleConnections() {
	for _, tr := range h.transports {
		tr.CloseIdleConnections()
	}
}

func (h *siteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &responseRecorder{ResponseWriter: w}
	var reqBytes atomic.Int64
	reqBytes.Store(requestHeaderBytes(r))
	h.active.Add(1)
	defer h.active.Add(-1)

	if !h.limiter.connectionAllowed(h.active.Load()) {
		w.Header().Set("Retry-After", "1")
		http.Error(rec, "site connection limit exceeded", http.StatusServiceUnavailable)
		h.record(r, start, rec, nil, http.StatusServiceUnavailable, reqBytes.Load())
		return
	}
	if h.limiter != nil {
		key := clientIP(r)
		if key == "" {
			key = "unknown"
		}
		if !h.limiter.rateAllowed(key, time.Now()) {
			w.Header().Set("Retry-After", "1")
			http.Error(rec, "site request rate limit exceeded", http.StatusTooManyRequests)
			h.record(r, start, rec, nil, http.StatusTooManyRequests, reqBytes.Load())
			return
		}
	}

	if h.ipDenied(clientIP(r)) {
		http.Error(rec, "forbidden by site IP access control", http.StatusForbidden)
		h.record(r, start, rec, nil, http.StatusForbidden, reqBytes.Load())
		return
	}

	node, err := h.up.bal.Next(r)
	if err != nil {
		http.Error(rec, "no healthy application", http.StatusBadGateway)
		h.record(r, start, rec, nil, http.StatusBadGateway, reqBytes.Load())
		return
	}
	node.Acquire()
	defer node.Release()
	node.Hit()

	ctx := r.Context()
	if t := h.site.ProxyTimeout; t > 0 && !isUpgrade(r) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t)
		defer cancel()
	}
	ctx = context.WithValue(ctx, nodeCtxKey{}, node)
	r = r.WithContext(ctx)

	if r.Body != nil && r.Body != http.NoBody {
		r.Body = &countingBody{rc: r.Body, n: &reqBytes}
	}
	if h.site.MaxBodySize > 0 {
		r.Body = http.MaxBytesReader(rec, r.Body, h.site.MaxBodySize)
	}

	h.proxy.ServeHTTP(rec, r)
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	if r.Context().Err() != nil && status >= 500 {
		status = 499
	}
	h.record(r, start, rec, node, status, reqBytes.Load())
}

func (h *siteHandler) ipDenied(addr string) bool {
	if h.ipMode == "" {
		return false
	}
	ip := net.ParseIP(addr)
	if h.ipMode == "allow" {
		if ip == nil || len(h.ipRules) == 0 {
			return true
		}
		for _, network := range h.ipRules {
			if network.Contains(ip) {
				return false
			}
		}
		return true
	}
	if ip == nil {
		return false
	}
	for _, network := range h.ipRules {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (h *siteHandler) record(r *http.Request, start time.Time, rec *responseRecorder, node *balancer.Node, status int, reqBytes int64) {
	if h.observer == nil {
		return
	}
	application := ""
	if node != nil && node.URL != nil {
		application = node.URL.Host
	}
	if reqBytes == 0 && r.ContentLength > 0 {
		reqBytes = r.ContentLength
	}
	ip := clientIP(r)
	rid, _ := r.Context().Value(requestIDCtxKey{}).(string)
	h.observer.RecordRequest(RequestEvent{
		Time:          start,
		SiteID:        h.site.ID,
		SiteName:      h.site.Name,
		Domain:        hostOnly(r.Host),
		ClientIP:      ip,
		Method:        r.Method,
		Path:          redactRequestURI(r.URL.RequestURI()),
		Protocol:      r.Proto,
		Status:        status,
		Duration:      time.Since(start),
		Upgraded:      isUpgrade(r),
		Application:   application,
		RequestBytes:  reqBytes,
		ResponseBytes: rec.bytes.Load() + responseHeaderBytes(rec.Header()),
		UserAgent:     r.UserAgent(),
		RequestID:     rid,
	})
}

func requestHeaderBytes(r *http.Request) int64 {
	n := len(r.Method) + 1 + len(r.URL.RequestURI()) + 1 + len(r.Proto) + 2 + len(r.Host) + 2
	for k, values := range r.Header {
		for _, v := range values {
			n += len(k) + 2 + len(v) + 2
		}
	}
	return int64(n + 2)
}

func responseHeaderBytes(h http.Header) int64 {
	n := len("HTTP/1.1 200 OK\r\n") + 2
	for k, values := range h {
		for _, v := range values {
			n += len(k) + 2 + len(v) + 2
		}
	}
	return int64(n)
}

// nodeTransport chooses the persistent transport of the node selected for the
// request, keeping connection pools per backend.
type nodeTransport struct {
	handlers *siteHandler
}

func (t nodeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	node, _ := req.Context().Value(nodeCtxKey{}).(*balancer.Node)
	if node == nil {
		return nil, errors.New("no application node in request context")
	}
	tr := t.handlers.transports[node.ID]
	if tr == nil {
		return nil, fmt.Errorf("no transport for application node %d", node.ID)
	}
	return tr.RoundTrip(req)
}

func newTransport(site SiteConfig, node *balancer.Node, cfg NodeConfig) *http.Transport {
	timeout := site.ProxyTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	verify := site.Options.UpstreamTLSVerify && cfg.TLSVerify
	serverName := site.Options.UpstreamSNI
	if serverName == "" {
		serverName = node.URL.Hostname()
	}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           safeDialContext,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: timeout,
		DisableKeepAlives:     !site.Options.KeepAlive,
		ForceAttemptHTTP2:     false,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: !verify, //nolint:gosec // explicit per-site option
			ServerName:         serverName,
		},
	}
}

func rewritePath(pr *httputil.ProxyRequest, strip, add string) {
	p := pr.Out.URL.Path
	if strip != "" && strings.HasPrefix(p, strip) {
		p = strings.TrimPrefix(p, strip)
		if p == "" || p[0] != '/' {
			p = "/" + p
		}
	}
	if add != "" {
		p = strings.TrimRight(add, "/") + p
	}
	if p == "" {
		p = "/"
	}
	pr.Out.URL.Path = p
	pr.Out.URL.RawPath = ""
}

func applyHeaders(h http.Header, rules []Header) {
	for _, rule := range rules {
		name := strings.TrimSpace(rule.Name)
		if name == "" {
			continue
		}
		if rule.Value == "" {
			h.Del(name)
			continue
		}
		h.Set(name, rule.Value)
	}
}

func writeStatus(w http.ResponseWriter, code int) {
	w.WriteHeader(code)
}

func isUpgrade(r *http.Request) bool {
	upgrade := strings.ToLower(strings.TrimSpace(r.Header.Get("Upgrade")))
	if upgrade != "websocket" && upgrade != "h2c" {
		return false
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

func requestLocalPort(r *http.Request) string {
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if _, port, err := net.SplitHostPort(a.String()); err == nil {
			return port
		}
	}
	return ""
}

type countingBody struct {
	rc io.ReadCloser
	n  *atomic.Int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.n.Add(int64(n))
	return n, err
}

func (b *countingBody) Close() error { return b.rc.Close() }

type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       atomic.Int64
	wroteHeader bool
	reqBytes    int64
}

func (r *responseRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes.Add(int64(n))
	return n, err
}

func (r *responseRecorder) Flush() {
	_ = http.NewResponseController(r.ResponseWriter).Flush()
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(r.ResponseWriter).Hijack()
}

func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// bufferPool implements httputil.BufferPool.
type bufferPool struct {
	size int
	pool sync.Pool
}

func newBufferPool(size int) *bufferPool {
	if size <= 0 {
		size = 32 * 1024
	}
	return &bufferPool{size: size}
}

func (b *bufferPool) Get() []byte {
	if v := b.pool.Get(); v != nil {
		return v.([]byte)
	}
	return make([]byte, b.size)
}

func (b *bufferPool) Put(buf []byte) {
	if cap(buf) == b.size {
		b.pool.Put(buf[:b.size])
	}
}

// noteFailure implements passive ejection when active checks are disabled.
func (u *applicationRuntime) noteFailure(node *balancer.Node, reason string) {
	if node == nil {
		return
	}
	limit := u.health.PassiveFailLimit
	if limit <= 0 {
		limit = 3
	}
	u.mu.Lock()
	if u.fails == nil {
		u.fails = make(map[uint]int)
	}
	u.fails[node.ID]++
	n := u.fails[node.ID]
	if n >= limit {
		u.fails[node.ID] = 0
	}
	active := u.health.Enabled
	u.mu.Unlock()
	if active || n < limit {
		return
	}
	if node.SetHealthy(false) && u.obs != nil {
		u.obs.ApplicationChanged(ApplicationEvent{
			ApplicationID: u.cfg.ID, NodeID: node.ID, Address: node.URL.String(),
			Healthy: false, Reason: reason, Time: time.Now(),
		})
	}
	cool := u.health.PassiveCooldown
	if cool <= 0 {
		cool = 30 * time.Second
	}
	time.AfterFunc(cool, func() {
		if node.SetHealthy(true) && u.obs != nil {
			u.obs.ApplicationChanged(ApplicationEvent{
				ApplicationID: u.cfg.ID, NodeID: node.ID, Address: node.URL.String(),
				Healthy: true, Reason: "passive cooldown elapsed", Time: time.Now(),
			})
		}
	})
}

func (u *applicationRuntime) noteSuccess(node *balancer.Node) {
	if node == nil || u.health.Enabled {
		return
	}
	u.mu.Lock()
	if u.fails != nil {
		delete(u.fails, node.ID)
	}
	u.mu.Unlock()
}
