package httpx

import (
	"strconv"
	"testing"
	"time"
)

// A caller chooses the keys, so a flood of distinct keys must not grow the
// map past its ceiling, and a key refused at the ceiling must not be kept.
func TestTokenBucketLimiterStaysUnderItsCeilingUnderAFloodOfKeys(t *testing.T) {
	limiter := NewTokenBucketLimiter(1, 1)
	now := time.Now()

	for i := 0; i < 60_000; i++ {
		limiter.AllowN("flood-"+strconv.Itoa(i), now, 1)
	}

	limiter.mu.Lock()
	size := len(limiter.buckets)
	_, kept := limiter.buckets["flood-59999"]
	limiter.mu.Unlock()

	if size > 50_000 {
		t.Fatalf("map holds %d keys, want at most the 50,000 ceiling", size)
	}
	if kept {
		t.Fatal("a key past the ceiling was tracked instead of refused")
	}
	if limiter.AllowN("a-new-caller", now, 1) {
		t.Fatal("a new key was admitted at the ceiling, so it cannot be limited")
	}
}
