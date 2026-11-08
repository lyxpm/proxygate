package proxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
)

// prepareRequest resolves the client address (honouring trusted proxies),
// assigns a request id and makes both available to handlers and logs.
func (e *Engine) prepareRequest(r *http.Request) *http.Request {
	ip := e.clientIP(r)
	if ip == "" {
		ip = remoteIP(r)
	}
	id := newRequestID()
	ctx := context.WithValue(r.Context(), clientIPCtxKey{}, ip)
	ctx = context.WithValue(ctx, requestIDCtxKey{}, id)
	r = r.WithContext(ctx)
	r.Header.Set("X-Request-ID", id)
	if ip != "" {
		_, port, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			port = "0"
		}
		r.RemoteAddr = net.JoinHostPort(ip, port)
	}
	return r
}

func (e *Engine) clientIP(r *http.Request) string {
	remote := remoteIP(r)
	ip := net.ParseIP(remote)
	if ip == nil {
		return remote
	}
	if !e.isTrusted(ip) {
		return ip.String()
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		parts := strings.Split(fwd, ",")
		candidate := ip
		for i := len(parts) - 1; i >= 0; i-- {
			p := net.ParseIP(strings.TrimSpace(parts[i]))
			if p == nil {
				break
			}
			candidate = p
			if !e.isTrusted(p) {
				return p.String()
			}
		}
		return candidate.String()
	}
	if real := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); real != nil {
		return real.String()
	}
	return ip.String()
}

func (e *Engine) isTrusted(ip net.IP) bool {
	for _, n := range e.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func clientIP(r *http.Request) string {
	if v, ok := r.Context().Value(clientIPCtxKey{}).(string); ok {
		return v
	}
	return remoteIP(r)
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}
