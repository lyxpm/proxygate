package balancer

import (
	"net/http"
	"testing"
)

func BenchmarkBalancerNext(b *testing.B) {
	for _, strategy := range []string{RoundRobin, WeightedRoundRobin, Failover, IPHashStrategy, LeastConnections} {
		b.Run(strategy, func(b *testing.B) {
			nodes := nodes(10)
			bal := New(strategy, nodes)
			req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
			req.RemoteAddr = "203.0.113.8:1234"
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := bal.Next(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkLeastConnectionsParallel(b *testing.B) {
	nodes := nodes(20)
	bal := New(LeastConnections, nodes)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
		for pb.Next() {
			if _, err := bal.Next(req); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
