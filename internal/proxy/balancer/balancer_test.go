package balancer

import (
	"net/http"
	"net/url"
	"testing"
)

func nodes(n int) []*Node {
	out := make([]*Node, 0, n)
	for i := 0; i < n; i++ {
		u, _ := url.Parse("http://127.0.0.1:9000")
		out = append(out, NewNode(uint(i+1), u, 1, 0))
	}
	return out
}

func TestIPHashStableAndIgnoresPort(t *testing.T) {
	n := nodes(3)
	b := New(IPHashStrategy, n)
	mk := func(addr string) *http.Request {
		r, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
		r.RemoteAddr = addr
		return r
	}
	first, err := b.Next(mk("203.0.113.8:1000"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Next(mk("203.0.113.8:2000"))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("ip hash changed for same host: %d vs %d", first.ID, second.ID)
	}
}

func TestLeastConnections(t *testing.T) {
	n := nodes(3)
	n[0].Acquire()
	n[0].Acquire()
	n[2].Acquire()
	b := New(LeastConnections, n)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	for i := 0; i < 4; i++ {
		node, err := b.Next(req)
		if err != nil {
			t.Fatal(err)
		}
		if node != n[1] {
			t.Fatalf("expected least-connected node, got %d", node.ID)
		}
	}
}

func TestRoundRobin(t *testing.T) {
	n := nodes(3)
	b := New(RoundRobin, n)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	seen := map[uint]int{}
	for i := 0; i < 9; i++ {
		node, err := b.Next(req)
		if err != nil {
			t.Fatal(err)
		}
		seen[node.ID]++
	}
	if len(seen) != 3 {
		t.Fatalf("expected all three nodes, got %v", seen)
	}
	for id, count := range seen {
		if count != 3 {
			t.Fatalf("node %d served %d times, want 3", id, count)
		}
	}
}

func TestRoundRobinSkipsUnhealthy(t *testing.T) {
	n := nodes(3)
	n[1].SetHealthy(false)
	b := New(RoundRobin, n)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	for i := 0; i < 6; i++ {
		node, err := b.Next(req)
		if err != nil {
			t.Fatal(err)
		}
		if node.ID == 2 {
			t.Fatal("unhealthy node selected")
		}
	}
}

func TestWeightedRoundRobin(t *testing.T) {
	n := nodes(3)
	n[0].Weight = 5
	n[1].Weight = 1
	n[2].Weight = 1
	b := New(WeightedRoundRobin, n)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	counts := map[uint]int{}
	for i := 0; i < 700; i++ {
		node, err := b.Next(req)
		if err != nil {
			t.Fatal(err)
		}
		counts[node.ID]++
	}
	if counts[1] < 450 || counts[2] < 80 || counts[3] < 80 {
		t.Fatalf("unexpected weighted distribution: %v", counts)
	}
}

func TestFailoverUsesLowestPriority(t *testing.T) {
	n := nodes(3)
	n[0].Priority, n[1].Priority, n[2].Priority = 0, 1, 1
	b := New(Failover, n)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	for i := 0; i < 5; i++ {
		node, err := b.Next(req)
		if err != nil {
			t.Fatal(err)
		}
		if node.ID != 1 {
			t.Fatalf("expected primary node, got %d", node.ID)
		}
	}
	n[0].SetHealthy(false)
	seen := map[uint]bool{}
	for i := 0; i < 4; i++ {
		node, err := b.Next(req)
		if err != nil {
			t.Fatal(err)
		}
		seen[node.ID] = true
	}
	if seen[1] || !seen[2] || !seen[3] {
		t.Fatalf("expected backup nodes only, got %v", seen)
	}
}

func TestNoHealthyNodes(t *testing.T) {
	n := nodes(2)
	n[0].SetHealthy(false)
	n[1].SetHealthy(false)
	for _, strategy := range []string{RoundRobin, WeightedRoundRobin, Failover} {
		if _, err := New(strategy, n).Next(nil); err != ErrNoHealthy {
			t.Fatalf("%s: expected ErrNoHealthy, got %v", strategy, err)
		}
	}
}
