package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/proxy/balancer"
)

func (e *Engine) checkLoop(ctx context.Context, up *applicationRuntime, node *balancer.Node, nc NodeConfig) {
	interval := up.health.Interval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	passes, fails := 0, 0
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		err := probeApplication(ctx, up, node, nc)
		if err == nil {
			fails = 0
			passes++
			if passes >= maxInt(up.health.HealthyThreshold, 1) && !node.Healthy() {
				node.SetHealthy(true)
				if e.observer != nil {
					e.observer.ApplicationChanged(ApplicationEvent{
						ApplicationID: up.cfg.ID, NodeID: node.ID, Address: node.URL.String(),
						Healthy: true, Reason: "health check passed", Time: time.Now(),
					})
				}
			}
		} else {
			passes = 0
			fails++
			if fails >= maxInt(up.health.UnhealthyThreshold, 1) && node.Healthy() {
				node.SetHealthy(false)
				if e.observer != nil {
					e.observer.ApplicationChanged(ApplicationEvent{
						ApplicationID: up.cfg.ID, NodeID: node.ID, Address: node.URL.String(),
						Healthy: false, Reason: err.Error(), Time: time.Now(),
					})
				}
			}
		}
		timer.Reset(interval)
	}
}

func probeApplication(ctx context.Context, up *applicationRuntime, node *balancer.Node, nc NodeConfig) error {
	timeout := up.health.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if up.health.Type == "tcp" {
		host := node.URL.Host
		if node.URL.Port() == "" {
			port := "80"
			if node.URL.Scheme == "https" {
				port = "443"
			}
			host = net.JoinHostPort(node.URL.Hostname(), port)
		}
		conn, err := safeDialer(timeout).DialContext(ctx, "tcp", host)
		if err != nil {
			return err
		}
		return conn.Close()
	}
	target := *node.URL
	target.Path = strings.TrimRight(node.URL.Path, "/") + "/" + strings.TrimLeft(up.health.Path, "/")
	if target.Path == "" {
		target.Path = "/"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ProxyManager-HealthCheck")
	client := up.healthClients[node.ID]
	if client == nil {
		client = newHealthClient(timeout, nc)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("health check returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func newHealthClient(timeout time.Duration, nc NodeConfig) *http.Client {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: safeDialContext,
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: !nc.TLSVerify, //nolint:gosec // per-node option
			},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       timeout,
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// validateHealthPath keeps the probe URL constructor simple.
func validateHealthPath(p string) error {
	if p == "" {
		return nil
	}
	u, err := url.Parse(p)
	if err != nil {
		return err
	}
	if u.IsAbs() {
		return errors.New("health check path must be relative")
	}
	return nil
}
