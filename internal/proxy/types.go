// Package proxy implements the reverse-proxy runtime. It has no dependency on
// the database or the HTTP API: callers build an immutable Config, the engine
// validates it and atomically swaps the live routing table.
package proxy

import (
	"crypto/tls"
	"errors"
	"time"
)

// Header is one request or response header rule. An empty Value deletes it.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SiteOptions covers per-site proxy behaviour.
type SiteOptions struct {
	StripPrefix       string   `json:"stripPrefix"`
	AddPrefix         string   `json:"addPrefix"`
	RequestHeaders    []Header `json:"requestHeaders"`
	ResponseHeaders   []Header `json:"responseHeaders"`
	UpstreamSNI       string   `json:"upstreamSni"`
	UpstreamTLSVerify bool     `json:"upstreamTlsVerify"`
	KeepAlive         bool     `json:"keepAlive"`
	UseUpstreamHost   bool     `json:"useUpstreamHost"`
	ForwardClientIP   bool     `json:"forwardClientIp"`
	BufferSize        int      `json:"bufferSize"`
	IPMode            string   `json:"ipMode"`
	IPRules           []string `json:"ipRules"`
}

// HealthConfig configures active and passive application health checks.
type HealthConfig struct {
	Enabled            bool
	Type               string // http | tcp
	Path               string
	Interval           time.Duration
	Timeout            time.Duration
	HealthyThreshold   int
	UnhealthyThreshold int
	PassiveFailLimit   int
	PassiveCooldown    time.Duration
}

// NodeConfig is one backend.
type NodeConfig struct {
	ID        uint
	Address   string // canonical URL, e.g. http://10.0.0.2:8080
	Weight    int
	Priority  int
	Enabled   bool
	Healthy   bool // last persisted state, used as initial runtime state
	TLSVerify bool
}

// ApplicationConfig is a load-balancing group.
type ApplicationConfig struct {
	ID       uint
	Name     string
	Strategy string
	Enabled  bool
	Nodes    []NodeConfig
	Health   HealthConfig
}

// SiteConfig is one virtual host.
type SiteConfig struct {
	ID            uint
	Name          string
	Enabled       bool
	ListenPort    int
	Domains       []string
	Protocol      string // http | https
	RedirectHTTPS bool
	ProxyTimeout  time.Duration
	MaxBodySize   int64
	Options       SiteOptions
	ApplicationID uint
	TLS           *tls.Certificate
	CertificateID *uint
}

// Config is a complete runtime snapshot.
type Config struct {
	Version      int64
	Applications map[uint]ApplicationConfig
	Sites        []SiteConfig
}

// ErrNoHealthy is re-exported for callers that map proxy errors to HTTP codes.
var ErrNoHealthy = errors.New("no healthy application node")

// ApplicationEvent reports a health transition.
type ApplicationEvent struct {
	ApplicationID uint
	NodeID        uint
	Address       string
	Healthy       bool
	Reason        string
	Time          time.Time
}

// RequestEvent is emitted after every proxied request (or local error).
type RequestEvent struct {
	Time          time.Time
	SiteID        uint
	SiteName      string
	Domain        string
	ClientIP      string
	Method        string
	Path          string
	Protocol      string
	Status        int
	Duration      time.Duration
	Upgraded      bool
	Application   string
	RequestBytes  int64
	ResponseBytes int64
	UserAgent     string
	RequestID     string
}

// Observer receives runtime events. Implementations must not block: the proxy
// calls them on the request path, so they should enqueue asynchronously.
type Observer interface {
	RecordRequest(RequestEvent)
	ApplicationChanged(ApplicationEvent)
}

// Status is a point-in-time engine snapshot for the API.
type Status struct {
	Running           bool              `json:"running"`
	StartedAt         time.Time         `json:"started_at"`
	ConfigVersion     int64             `json:"config_version"`
	LastApply         time.Time         `json:"last_apply"`
	LastError         string            `json:"last_error,omitempty"`
	ActiveConnections int64             `json:"active_connections"`
	Ports             []PortStatus      `json:"ports"`
	Applications      []ApplicationView `json:"applications"`
	Sites             []SiteStatus      `json:"sites"`
}

// SiteStatus reports live per-site state.
type SiteStatus struct {
	ID     uint  `json:"id"`
	Active int64 `json:"active_connections"`
}

// PortStatus describes one listening socket.
type PortStatus struct {
	Port  int  `json:"port"`
	TLS   bool `json:"tls"`
	Sites int  `json:"sites"`
}

// ApplicationView reports runtime health for one application group.
type ApplicationView struct {
	ID       uint         `json:"id"`
	Name     string       `json:"name"`
	Strategy string       `json:"strategy"`
	Nodes    []NodeStatus `json:"nodes"`
}

// NodeStatus is the live state of one backend node.
type NodeStatus struct {
	ID       uint   `json:"id"`
	Address  string `json:"address"`
	Healthy  bool   `json:"healthy"`
	Active   int64  `json:"active_connections"`
	Requests int64  `json:"requests"`
}
