package proxy

import (
	"sync"
	"time"
)

const (
	maxRateBucketsPerSite = 10000
	rateBucketIdleTTL     = 10 * time.Minute
	rateCleanupInterval   = time.Minute
)

type rateBucket struct {
	tokens   float64
	last     time.Time
	lastSeen time.Time
}

// siteLimiter holds per-site concurrency and per-IP token buckets. All limits
// are opt-in; a nil limiter means the feature is disabled for the site.
type siteLimiter struct {
	connectionLimit int64
	ratePerSecond   float64
	rateBurst       float64

	mu          sync.Mutex
	buckets     map[string]*rateBucket
	lastCleanup time.Time
}

func newSiteLimiter(opts SiteOptions) *siteLimiter {
	if !opts.ConnectionLimitEnabled && !opts.RateLimitEnabled {
		return nil
	}
	return &siteLimiter{
		connectionLimit: int64(opts.ConnectionLimit),
		ratePerSecond:   float64(opts.RateLimitPerSecond),
		rateBurst:       float64(opts.RateLimitBurst),
		buckets:         make(map[string]*rateBucket),
		lastCleanup:     time.Now(),
	}
}

func (l *siteLimiter) connectionAllowed(active int64) bool {
	return l == nil || l.connectionLimit <= 0 || active <= l.connectionLimit
}

func (l *siteLimiter) rateAllowed(key string, now time.Time) bool {
	if l == nil || l.ratePerSecond <= 0 || l.rateBurst <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastCleanup) >= rateCleanupInterval {
		for k, b := range l.buckets {
			if now.Sub(b.lastSeen) > rateBucketIdleTTL {
				delete(l.buckets, k)
			}
		}
		l.lastCleanup = now
	}
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= maxRateBucketsPerSite {
			// Evict one stale-ish entry instead of growing without bound.
			for k, candidate := range l.buckets {
				if now.Sub(candidate.lastSeen) > rateBucketIdleTTL {
					delete(l.buckets, k)
					break
				}
			}
			if len(l.buckets) >= maxRateBucketsPerSite {
				return false
			}
		}
		b = &rateBucket{tokens: l.rateBurst, last: now}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens += elapsed * l.ratePerSecond
			if b.tokens > l.rateBurst {
				b.tokens = l.rateBurst
			}
			b.last = now
		}
	}
	b.lastSeen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
