package proxy

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"

	"golang.org/x/net/http2"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

type testObserver struct {
	requests chan RequestEvent
	events   chan ApplicationEvent
}

func newTestObserver() *testObserver {
	return &testObserver{requests: make(chan RequestEvent, 100), events: make(chan ApplicationEvent, 100)}
}

func (o *testObserver) RecordRequest(ev RequestEvent) {
	select {
	case o.requests <- ev:
	default:
	}
}

func (o *testObserver) ApplicationChanged(ev ApplicationEvent) {
	select {
	case o.events <- ev:
	default:
	}
}

func backend(t *testing.T, name string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", name)
		w.Header().Set("X-Custom-Resp", "ok")
		fmt.Fprintf(w, "backend=%s host=%s xff=%s real=%s custom=%s", name, r.Host, r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Real-IP"), r.Header.Get("X-Custom-Req"))
	}))
}

func TestEngineRoutesByHostAndRewritesHeaders(t *testing.T) {
	port := freePort(t)
	b1, b2 := backend(t, "one"), backend(t, "two")
	defer b1.Close()
	defer b2.Close()

	obs := newTestObserver()
	e := New(Options{Observer: obs})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.Stop(context.Background())

	cfg := &Config{
		Version: 1,
		Applications: map[uint]ApplicationConfig{
			1: {ID: 1, Name: "u1", Enabled: true, Nodes: []NodeConfig{{ID: 1, Address: b1.URL, Enabled: true, Healthy: true, TLSVerify: true}}},
			2: {ID: 2, Name: "u2", Enabled: true, Nodes: []NodeConfig{{ID: 2, Address: b2.URL, Enabled: true, Healthy: true, TLSVerify: true}}},
		},
		Sites: []SiteConfig{
			{ID: 1, Name: "a", Enabled: true, ListenPort: port, Domains: []string{"a.test"}, Protocol: "http", ApplicationID: 1,
				ProxyTimeout: 5 * time.Second,
				Options:      SiteOptions{KeepAlive: true, UpstreamTLSVerify: true, ForwardClientIP: true, RequestHeaders: []Header{{Name: "X-Custom-Req", Value: "custom"}}}},
			{ID: 2, Name: "b", Enabled: true, ListenPort: port, Domains: []string{"b.test"}, Protocol: "http", ApplicationID: 2,
				ProxyTimeout: 5 * time.Second, Options: SiteOptions{KeepAlive: true, UpstreamTLSVerify: true, ForwardClientIP: true}},
			{ID: 3, Name: "c", Enabled: true, ListenPort: port, Domains: []string{"c.test"}, Protocol: "http", ApplicationID: 1,
				ProxyTimeout: 5 * time.Second, Options: SiteOptions{KeepAlive: true, UpstreamTLSVerify: true, ForwardClientIP: false}},
		},
	}
	if err := e.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}

	get := func(host string) (*http.Response, string) {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
		req.Host = host
		req.Header.Set("X-Forwarded-For", "203.0.113.9")
		req.Header.Set("X-Real-IP", "203.0.113.9")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp, string(raw)
	}
	resp, body := get("a.test")
	if resp.StatusCode != 200 || !strings.Contains(body, "backend=one") {
		t.Fatalf("a.test: status=%d body=%q", resp.StatusCode, body)
	}
	if !strings.Contains(body, "xff=127.0.0.1") || strings.Contains(body, "real=127.0.0.1") || !strings.Contains(body, "custom=custom") {
		t.Fatalf("forwarded headers not applied correctly: %q", body)
	}
	if resp.Header.Get("X-Custom-Resp") != "ok" {
		t.Fatal("response header rule not applied")
	}
	_, body = get("b.test")
	if !strings.Contains(body, "backend=two") {
		t.Fatalf("b.test routed incorrectly: %q", body)
	}
	_, body = get("c.test")
	if strings.Contains(body, "xff=127.0.0.1") || strings.Contains(body, "xff=203.0.113.9") || strings.Contains(body, "real=127.0.0.1") {
		t.Fatalf("disabled XFF was forwarded: %q", body)
	}
	resp, _ = get("unknown.test")
	if resp.StatusCode != 404 {
		t.Fatalf("unknown host status=%d, want 404", resp.StatusCode)
	}

	// Hot swap: site a now points at application 2, no listener restart.
	cfg.Version = 2
	cfg.Sites[0].ApplicationID = 2
	if err := e.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	_, body = get("a.test")
	if !strings.Contains(body, "backend=two") {
		t.Fatalf("hot swap not applied: %q", body)
	}
	select {
	case ev := <-obs.requests:
		if ev.SiteID != 1 || ev.Status != 200 {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no request event")
	}
}

func TestEngineHealthCheckMarksNodeDown(t *testing.T) {
	port := freePort(t)
	var fail atomic.Bool
	application := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer application.Close()

	e := New(Options{})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.Stop(context.Background())
	cfg := &Config{
		Version: 1,
		Applications: map[uint]ApplicationConfig{1: {
			ID: 1, Name: "u", Enabled: true,
			Nodes: []NodeConfig{{ID: 1, Address: application.URL, Enabled: true, Healthy: true, TLSVerify: true}},
			Health: HealthConfig{Enabled: true, Type: "http", Path: "/", Interval: 30 * time.Millisecond, Timeout: time.Second,
				HealthyThreshold: 1, UnhealthyThreshold: 1},
		}},
		Sites: []SiteConfig{{ID: 1, Name: "s", Enabled: true, ListenPort: port, Domains: []string{"h.test"}, Protocol: "http",
			ApplicationID: 1, ProxyTimeout: 2 * time.Second, Options: SiteOptions{KeepAlive: true, UpstreamTLSVerify: true}}},
	}
	if err := e.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st := e.Status()
		if len(st.Applications) == 1 && len(st.Applications[0].Nodes) == 1 && !st.Applications[0].Nodes[0].Healthy {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
	req.Host = "h.test"
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502 when all nodes down", resp.StatusCode)
	}
	fail.Store(false)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st := e.Status()
		if st.Applications[0].Nodes[0].Healthy {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	resp, err = (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d after recovery", resp.StatusCode)
	}
}

func testCertificate(t *testing.T, host string) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	pair.Leaf = leaf
	return &pair
}

func TestEngineTLSAndSNI(t *testing.T) {
	port := freePort(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tls-ok"))
	}))
	defer backend.Close()
	pair := testCertificate(t, "secure.test")
	e := New(Options{})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.Stop(context.Background())
	cfg := &Config{
		Version: 1,
		Applications: map[uint]ApplicationConfig{1: {
			ID: 1, Name: "u", Enabled: true,
			Nodes: []NodeConfig{{ID: 1, Address: backend.URL, Enabled: true, Healthy: true, TLSVerify: true}},
		}},
		Sites: []SiteConfig{{ID: 1, Name: "secure", Enabled: true, ListenPort: port, Domains: []string{"secure.test"},
			Protocol: "https", ApplicationID: 1, TLS: pair, ProxyTimeout: 2 * time.Second,
			Options: SiteOptions{KeepAlive: true, UpstreamTLSVerify: true}}},
	}
	if err := e.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, ServerName: "secure.test",
	}}}
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("https://127.0.0.1:%d/", port), nil)
	req.Host = "secure.test"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "tls-ok" {
		t.Fatalf("body=%q", body)
	}
}

func TestEngineHTTP2(t *testing.T) {
	port := freePort(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("h2-ok"))
	}))
	defer backend.Close()
	pair := testCertificate(t, "h2.test")
	e := New(Options{})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.Stop(context.Background())
	cfg := &Config{
		Version: 1,
		Applications: map[uint]ApplicationConfig{1: {
			ID: 1, Name: "u", Enabled: true,
			Nodes: []NodeConfig{{ID: 1, Address: backend.URL, Enabled: true, Healthy: true, TLSVerify: true}},
		}},
		Sites: []SiteConfig{{ID: 1, Name: "h2", Enabled: true, ListenPort: port, Domains: []string{"h2.test"},
			Protocol: "https", ApplicationID: 1, TLS: pair, ProxyTimeout: 2 * time.Second,
			Options: SiteOptions{KeepAlive: true, UpstreamTLSVerify: true}}},
	}
	if err := e.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http2.Transport{TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, ServerName: "h2.test",
		}},
	}
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("https://127.0.0.1:%d/", port), nil)
	req.Host = "h2.test"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.ProtoMajor != 2 || string(body) != "h2-ok" {
		t.Fatalf("proto=%s body=%q", resp.Proto, body)
	}
}

func TestEngineHTTP3(t *testing.T) {
	port := freePort(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("h3-ok"))
	}))
	defer backend.Close()
	pair := testCertificate(t, "h3.test")
	e := New(Options{})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.Stop(context.Background())
	cfg := &Config{
		Version: 1,
		Applications: map[uint]ApplicationConfig{1: {
			ID: 1, Name: "u", Enabled: true,
			Nodes: []NodeConfig{{ID: 1, Address: backend.URL, Enabled: true, Healthy: true, TLSVerify: true}},
		}},
		Sites: []SiteConfig{{ID: 1, Name: "h3", Enabled: true, ListenPort: port, Domains: []string{"h3.test"},
			Protocol: "https", ApplicationID: 1, TLS: pair, ProxyTimeout: 2 * time.Second,
			Options: SiteOptions{KeepAlive: true, UpstreamTLSVerify: true}}},
	}
	if err := e.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	tr := &http3.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "h3.test"}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Timeout: 5 * time.Second, Transport: tr}
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("https://127.0.0.1:%d/", port), nil)
	req.Host = "h3.test"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.ProtoMajor != 3 || string(body) != "h3-ok" {
		t.Fatalf("proto=%s body=%q", resp.Proto, body)
	}
}

func TestEngineSiteIPAccessControl(t *testing.T) {
	port := freePort(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ip-ok"))
	}))
	defer backend.Close()
	e := New(Options{})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.Stop(context.Background())
	base := Config{
		Version: 1,
		Applications: map[uint]ApplicationConfig{1: {
			ID: 1, Name: "u", Enabled: true,
			Nodes: []NodeConfig{{ID: 1, Address: backend.URL, Enabled: true, Healthy: true, TLSVerify: true}},
		}},
		Sites: []SiteConfig{{ID: 1, Name: "ip", Enabled: true, ListenPort: port, Domains: []string{"ip.test"},
			Protocol: "http", ApplicationID: 1, ProxyTimeout: 2 * time.Second,
			Options: SiteOptions{KeepAlive: true, UpstreamTLSVerify: true, IPMode: "allow", IPRules: []string{"10.0.0.0/8"}}}},
	}
	if err := e.Apply(&base); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	do := func() int {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
		req.Host = "ip.test"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if code := do(); code != http.StatusForbidden {
		t.Fatalf("allow list without loopback status=%d", code)
	}
	base.Version = 2
	base.Sites[0].Options.IPRules = []string{"127.0.0.1/32"}
	if err := e.Apply(&base); err != nil {
		t.Fatal(err)
	}
	if code := do(); code != http.StatusOK {
		t.Fatalf("allow loopback status=%d", code)
	}
	base.Version = 3
	base.Sites[0].Options.IPMode = "deny"
	if err := e.Apply(&base); err != nil {
		t.Fatal(err)
	}
	if code := do(); code != http.StatusForbidden {
		t.Fatalf("deny loopback status=%d", code)
	}
}

func TestEngineProxiesWebSocket(t *testing.T) {
	port := freePort(t)
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected websocket", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		key := r.Header.Get("Sec-WebSocket-Key")
		sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		accept := base64.StdEncoding.EncodeToString(sum[:])
		fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
		if err := rw.Flush(); err != nil {
			return
		}
		op, payload, err := readFrame(rw.Reader)
		if err != nil {
			return
		}
		_ = writeFrame(rw.Writer, op, payload)
		_ = rw.Flush()
	}))
	defer echo.Close()

	e := New(Options{})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.Stop(context.Background())
	cfg := &Config{
		Version: 1,
		Applications: map[uint]ApplicationConfig{1: {
			ID: 1, Name: "ws", Enabled: true,
			Nodes: []NodeConfig{{ID: 1, Address: echo.URL, Enabled: true, Healthy: true, TLSVerify: true}},
		}},
		Sites: []SiteConfig{{ID: 1, Name: "ws", Enabled: true, ListenPort: port, Domains: []string{"ws.test"}, Protocol: "http",
			ApplicationID: 1, ProxyTimeout: 30 * time.Second, Options: SiteOptions{KeepAlive: true, UpstreamTLSVerify: true}}},
	}
	if err := e.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: ws.test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", key)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	bw := bufio.NewWriter(conn)
	if err := writeFrame(bw, 1, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	op, payload, err := readFrame(br)
	if err != nil {
		t.Fatal(err)
	}
	if op != 1 || string(payload) != "hello" {
		t.Fatalf("echo mismatch op=%d payload=%q", op, payload)
	}
}

func writeFrame(w io.Writer, op byte, payload []byte) error {
	if _, err := w.Write([]byte{0x80 | op}); err != nil {
		return err
	}
	var head []byte
	switch {
	case len(payload) < 126:
		head = []byte{byte(len(payload))}
	case len(payload) <= 65535:
		head = []byte{126, byte(len(payload) >> 8), byte(len(payload))}
	default:
		head = make([]byte, 9)
		head[0] = 127
		binary.BigEndian.PutUint64(head[1:], uint64(len(payload)))
	}
	if _, err := w.Write(head); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r *bufio.Reader) (byte, []byte, error) {
	h1, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	h2, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	op := h1 & 0x0f
	masked := h2&0x80 != 0
	n := int64(h2 & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = int64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = int64(binary.BigEndian.Uint64(b[:]))
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return op, payload, nil
}
