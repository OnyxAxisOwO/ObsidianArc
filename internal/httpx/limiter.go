package httpx

import (
	"sync"
	"time"
)

type tokenBucket struct {
	tokens     float64
	lastUpdate time.Time
}

// maxBuckets bounds the map. Callers choose the keys (a leaderboard keys on the
// client address and the account), so the number of keys is whatever a caller
// can produce, and memory must not follow it. The pow tracker keeps the same
// ceiling for the same reason.
const maxBuckets = 50_000

// TokenBucketLimiter throttles requests using a token bucket algorithm.
// It is stored in memory: analytics dashboards and leaderboards do not warrant
// database-backed state, and dropping counts across a process restart is an
// acceptable tradeoff to keep the deployment free of auxiliary cache tiers.
type TokenBucketLimiter struct {
	rate      float64
	burst     float64
	mu        sync.Mutex
	buckets   map[string]*tokenBucket
	lastSwept time.Time
}

func NewTokenBucketLimiter(rate, burst float64) *TokenBucketLimiter {
	return &TokenBucketLimiter{
		rate:      rate,
		burst:     burst,
		buckets:   make(map[string]*tokenBucket),
		lastSwept: time.Now(),
	}
}

// Allow reports whether a single event is permitted under the rate limit.
func (l *TokenBucketLimiter) Allow(key string) bool {
	return l.AllowN(key, time.Now(), 1.0)
}

// AllowN reports whether n events are permitted at the given timestamp.
func (l *TokenBucketLimiter) AllowN(key string, now time.Time, n float64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Sweep lazily on the write path rather than starting a standing background goroutine.
	if now.Sub(l.lastSwept) > 5*time.Minute {
		l.sweepLocked(now)
	}

	b, ok := l.buckets[key]
	if !ok {
		// Checked before the insert, so a refused key leaves nothing behind.
		// At the ceiling the idle keys are dropped first, but the walk runs at
		// most once a second: a flood of new keys must not make every request
		// pay for a scan. Still full after that, the key is refused rather than
		// admitted untracked, since a caller who cannot be counted cannot be
		// limited.
		if len(l.buckets) >= maxBuckets {
			if now.Sub(l.lastSwept) >= time.Second {
				l.sweepLocked(now)
			}
			if len(l.buckets) >= maxBuckets {
				return false
			}
		}
		b = &tokenBucket{
			tokens:     l.burst,
			lastUpdate: now,
		}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.lastUpdate).Seconds()
		if elapsed > 0 {
			b.tokens += elapsed * l.rate
			if b.tokens > l.burst {
				b.tokens = l.burst
			}
			b.lastUpdate = now
		}
	}

	if b.tokens >= n {
		b.tokens -= n
		return true
	}
	return false
}

func (l *TokenBucketLimiter) sweepLocked(now time.Time) {
	l.lastSwept = now
	threshold := 10 * time.Minute
	// Under high cardinality loads, prune aggressively so memory remains bounded.
	if len(l.buckets) > 10000 {
		threshold = time.Minute
	}
	for k, b := range l.buckets {
		if now.Sub(b.lastUpdate) > threshold {
			delete(l.buckets, k)
		}
	}
}
