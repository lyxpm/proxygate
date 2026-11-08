package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func benchmarkFreePort(b *testing.B) int {
	b.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func BenchmarkEngineHTTP1(b *testing.B) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()
	port := benchmarkFreePort(b)
	e := New(Options{})
	if err := e.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	defer e.Stop(context.Background())
	cfg := &Config{
		Version: 1,
		Applications: map[uint]ApplicationConfig{1: {
			ID: 1, Name: "bench", Enabled: true,
			Nodes: []NodeConfig{{ID: 1, Address: backend.URL, Enabled: true, Healthy: true, TLSVerify: true}},
		}},
		Sites: []SiteConfig{{ID: 1, Name: "bench", Enabled: true, ListenPort: port, Domains: []string{"bench.test"},
			Protocol: "http", ApplicationID: 1, ProxyTimeout: 5 * time.Second,
			Options: SiteOptions{KeepAlive: true, UpstreamTLSVerify: true}}},
	}
	if err := e.Apply(cfg); err != nil {
		b.Fatal(err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		tr := &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 64}
		defer tr.CloseIdleConnections()
		client := &http.Client{Timeout: 5 * time.Second, Transport: tr}
		for pb.Next() {
			req, _ := http.NewRequest(http.MethodGet, url, nil)
			req.Host = "bench.test"
			resp, err := client.Do(req)
			if err != nil {
				b.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	})
}
