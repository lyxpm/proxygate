// Package balancer implements application selection. Nodes keep their own health
// and connection counters so every strategy sees a consistent view.
package balancer

import (
	"errors"
	"hash/fnv"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"sync/atomic"
)

// ErrNoHealthy is returned when every node is disabled or unhealthy.
var ErrNoHealthy = errors.New("no healthy application node")

// Strategies supported by the first version.
const (
	RoundRobin         = "round_robin"
	WeightedRoundRobin = "weighted_round_robin"
	Failover           = "failover"
	IPHashStrategy     = "ip_hash"
	LeastConnections   = "least_connections"
)

// Node is a runtime backend.
type Node struct {
	ID       uint
	URL      *url.URL
	Weight   int
	Priority int
	Enabled  bool

	healthy  atomic.Bool
	conns    atomic.Int64
	requests atomic.Int64
}

// NewNode creates a node; weight is clamped to at least 1.
func NewNode(id uint, u *url.URL, weight, priority int) *Node {
	if weight < 1 {
		weight = 1
	}
	n := &Node{ID: id, URL: u, Weight: weight, Priority: priority, Enabled: true}
	n.healthy.Store(true)
	return n
}

// Healthy reports the current health flag.
func (n *Node) Healthy() bool { return n.healthy.Load() }

// SetHealthy updates the health flag and returns true when it changed.
func (n *Node) SetHealthy(v bool) bool { return n.healthy.Swap(v) != v }

// Acquire increments the active connection count.
func (n *Node) Acquire() { n.conns.Add(1) }

// Release decrements the active connection count.
func (n *Node) Release() { n.conns.Add(-1) }

// Conns returns the active connection count.
func (n *Node) Conns() int64 { return n.conns.Load() }

// Hit increments the served-request counter.
func (n *Node) Hit() { n.requests.Add(1) }

// Requests returns the number of requests served by this node.
func (n *Node) Requests() int64 { return n.requests.Load() }

// Balancer selects a node for a request.
type Balancer interface {
	Next(req *http.Request) (*Node, error)
}

// New builds the strategy named by strategy. Unknown names fall back to
// round-robin so old configurations keep working after an upgrade.
func New(strategy string, nodes []*Node) Balancer {
	switch strategy {
	case WeightedRoundRobin:
		return &weighted{nodes: nodes}
	case Failover:
		return &failover{nodes: nodes}
	case IPHashStrategy:
		return &IPHash{nodes: nodes}
	case LeastConnections:
		return &leastConnections{nodes: nodes}
	default:
		return &roundRobin{nodes: nodes}
	}
}

func healthyCount(nodes []*Node) int {
	n := 0
	for _, node := range nodes {
		if node.Healthy() {
			n++
		}
	}
	return n
}

type roundRobin struct {
	nodes []*Node
	next  atomic.Uint64
}

func (b *roundRobin) Next(_ *http.Request) (*Node, error) {
	healthy := make([]*Node, 0, len(b.nodes))
	for _, n := range b.nodes {
		if n.Healthy() {
			healthy = append(healthy, n)
		}
	}
	if len(healthy) == 0 {
		return nil, ErrNoHealthy
	}
	i := b.next.Add(1) - 1
	return healthy[int(i%uint64(len(healthy)))], nil
}

type weighted struct {
	mu    sync.Mutex
	nodes []*Node
	cur   map[*Node]int
}

func (b *weighted) Next(_ *http.Request) (*Node, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cur == nil {
		b.cur = make(map[*Node]int, len(b.nodes))
	}
	total := 0
	var best *Node
	for _, n := range b.nodes {
		if !n.Healthy() {
			continue
		}
		b.cur[n] += n.Weight
		total += n.Weight
		if best == nil || b.cur[n] > b.cur[best] {
			best = n
		}
	}
	if best == nil {
		return nil, ErrNoHealthy
	}
	b.cur[best] -= total
	return best, nil
}

type failover struct {
	nodes []*Node
	next  atomic.Uint64
}

func (b *failover) Next(_ *http.Request) (*Node, error) {
	best := 0
	var candidates []*Node
	for _, n := range b.nodes {
		if !n.Healthy() {
			continue
		}
		if len(candidates) == 0 || n.Priority < best {
			best = n.Priority
			candidates = candidates[:0]
			candidates = append(candidates, n)
		} else if n.Priority == best {
			candidates = append(candidates, n)
		}
	}
	if len(candidates) == 0 {
		return nil, ErrNoHealthy
	}
	i := b.next.Add(1) - 1
	return candidates[int(i%uint64(len(candidates)))], nil
}

// IPHash is provided for the follow-up version but not wired to the API yet.
type IPHash struct {
	nodes []*Node
	mu    sync.Mutex
}

// IPHash selects a node by client IP.
func (b *IPHash) Next(req *http.Request) (*Node, error) {
	healthy := make([]*Node, 0, len(b.nodes))
	for _, n := range b.nodes {
		if n.Healthy() {
			healthy = append(healthy, n)
		}
	}
	if len(healthy) == 0 {
		return nil, ErrNoHealthy
	}
	host := req.RemoteAddr
	if h, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		host = h
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(host))
	return healthy[int(h.Sum64()%uint64(len(healthy)))], nil
}

type leastConnections struct {
	nodes []*Node
	next  atomic.Uint64
}

func (b *leastConnections) Next(_ *http.Request) (*Node, error) {
	healthy := make([]*Node, 0, len(b.nodes))
	for _, n := range b.nodes {
		if n.Healthy() {
			healthy = append(healthy, n)
		}
	}
	if len(healthy) == 0 {
		return nil, ErrNoHealthy
	}
	best := healthy[0]
	for _, n := range healthy[1:] {
		if n.Conns() < best.Conns() {
			best = n
		}
	}
	// Break ties deterministically so identical idle nodes are spread.
	tied := make([]*Node, 0, len(healthy))
	for _, n := range healthy {
		if n.Conns() == best.Conns() {
			tied = append(tied, n)
		}
	}
	i := b.next.Add(1) - 1
	return tied[int(i%uint64(len(tied)))], nil
}

// SortByPriority returns nodes ordered by priority then weight, for display.
func SortByPriority(nodes []*Node) []*Node {
	out := append([]*Node(nil), nodes...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Random picks a healthy node uniformly (used by tests and diagnostics).
func Random(nodes []*Node) *Node {
	healthy := make([]*Node, 0, len(nodes))
	for _, n := range nodes {
		if n.Healthy() {
			healthy = append(healthy, n)
		}
	}
	if len(healthy) == 0 {
		return nil
	}
	return healthy[rand.Intn(len(healthy))]
}
