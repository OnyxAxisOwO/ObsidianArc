package httpx

import (
	"testing"
	"time"
)

func TestTokenBucketLimiterBurstAndReplenish(t *testing.T) {
	limiter := NewTokenBucketLimiter(2, 5) // 2 tokens/sec, burst 5
	key := "user-1"
	now := time.Now()

	// Initial burst: 5 tokens allowed
	for i := 0; i < 5; i++ {
		if !limiter.AllowN(key, now, 1.0) {
			t.Fatalf("request %d should be allowed within burst", i+1)
		}
	}

	// 6th request at the same instant must be rejected
	if limiter.AllowN(key, now, 1.0) {
		t.Fatal("request 6 at the same instant should be throttled")
	}

	// After 500ms, 1 token is replenished (2 tokens/sec * 0.5s = 1.0)
	now = now.Add(500 * time.Millisecond)
	if !limiter.AllowN(key, now, 1.0) {
		t.Fatal("request after 500ms should be allowed")
	}
	if limiter.AllowN(key, now, 1.0) {
		t.Fatal("second request after 500ms should be throttled")
	}

	// Different keys must have independent buckets
	otherKey := "user-2"
	if !limiter.AllowN(otherKey, now, 1.0) {
		t.Fatal("different key should have its own full bucket")
	}
}

func TestTokenBucketLimiterSweep(t *testing.T) {
	limiter := NewTokenBucketLimiter(2, 5)
	now := time.Now()
	limiter.AllowN("old-user", now, 1.0)

	// After 11 minutes of inactivity, sweeping removes the old bucket
	now = now.Add(11 * time.Minute)
	limiter.AllowN("new-user", now, 1.0)

	limiter.mu.Lock()
	_, oldExists := limiter.buckets["old-user"]
	_, newExists := limiter.buckets["new-user"]
	limiter.mu.Unlock()

	if oldExists {
		t.Error("old-user bucket should have been swept")
	}
	if !newExists {
		t.Error("new-user bucket should exist")
	}
}

func TestTokenBucketLimiterAdaptiveSweep(t *testing.T) {
	limiter := NewTokenBucketLimiter(2, 5)
	now := time.Now()

	// Populate 10,001 buckets with 2-minute-old timestamps
	oldTime := now.Add(-2 * time.Minute)
	limiter.mu.Lock()
	for i := 0; i <= 10000; i++ {
		limiter.buckets[string(rune(i))] = &tokenBucket{
			tokens:     5,
			lastUpdate: oldTime,
		}
	}
	limiter.mu.Unlock()

	// Trigger sweep
	now = now.Add(6 * time.Minute)
	limiter.AllowN("fresh-user", now, 1.0)

	limiter.mu.Lock()
	count := len(limiter.buckets)
	limiter.mu.Unlock()

	// All 10,001 older-than-1-minute buckets should have been swept aggressively
	if count != 1 {
		t.Fatalf("expected adaptive sweep to leave only 1 bucket, got %d", count)
	}
}
