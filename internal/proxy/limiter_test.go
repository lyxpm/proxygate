package proxy

import (
	"testing"
	"time"
)

func TestSiteLimiterRateAndBurst(t *testing.T) {
	l := newSiteLimiter(SiteOptions{
		RateLimitEnabled: true, RateLimitPerSecond: 1, RateLimitBurst: 2,
	})
	now := time.Now()
	if !l.rateAllowed("192.0.2.1", now) || !l.rateAllowed("192.0.2.1", now) {
		t.Fatal("initial burst should be allowed")
	}
	if l.rateAllowed("192.0.2.1", now) {
		t.Fatal("third immediate request should be rejected")
	}
	if !l.rateAllowed("192.0.2.1", now.Add(time.Second)) {
		t.Fatal("bucket should refill")
	}
	if !l.rateAllowed("192.0.2.2", now) {
		t.Fatal("different IP should have its own bucket")
	}
}

func TestSiteLimiterConnectionLimit(t *testing.T) {
	l := newSiteLimiter(SiteOptions{ConnectionLimitEnabled: true, ConnectionLimit: 2})
	if !l.connectionAllowed(2) || l.connectionAllowed(3) {
		t.Fatal("unexpected connection limit result")
	}
	if !(nilSite()).connectionAllowed(9999) {
		t.Fatal("nil limiter should allow")
	}
}

func nilSite() *siteLimiter { return nil }
