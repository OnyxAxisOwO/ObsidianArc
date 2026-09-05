package auth

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Limiter throttles credential guessing.
//
// It is in memory on purpose. The alternative — a counter table — would turn
// every failed login into a database write, and the alternative to that is a
// cache tier this project does not want. One process owns the login endpoint,
// so a map is both correct and free. A restart forgives outstanding
// penalties, which is an acceptable trade for keeping the deployment at one
// binary.
//
// Two keys are tracked for each attempt: the caller's address, so one host
// cannot spray many accounts, and the account being targeted, so a botnet
// cannot spread its guesses across addresses.
type Limiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSwept time.Time
}

type bucket struct {
	failures int
	// When the next attempt is permitted. Zero means "now".
	blockedUntil time.Time
	lastFailure  time.Time
}

const (
	// Attempts allowed before a delay is imposed. Generous enough that a
	// person mistyping a password never notices.
	freeAttempts = 5
	// How long a bucket survives with no activity.
	bucketTTL = 30 * time.Minute
	// Ceiling on the exponential backoff.
	maxBackoff = 15 * time.Minute
	// How often the map is swept for dead entries. Bounded work, done on the
	// write path, so there is no timer goroutine for this.
	sweepInterval = 5 * time.Minute
)

func NewLimiter() *Limiter {
	return &Limiter{buckets: map[string]*bucket{}, lastSwept: time.Now()}
}

// RateLimitError carries how long the caller must wait, so the handler can
// send a Retry-After the client can act on.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("too many attempts; try again in %s", e.RetryAfter.Round(time.Second))
}

// Allow reports whether an attempt may proceed.
func (l *Limiter) Allow(ip, identifier string) error {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)

	for _, key := range keys(ip, identifier) {
		entry := l.buckets[key]
		if entry == nil {
			continue
		}
		if wait := entry.blockedUntil.Sub(now); wait > 0 {
			return &RateLimitError{RetryAfter: wait}
		}
	}
	return nil
}

// Fail records a rejected attempt and lengthens the wait for the next one.
func (l *Limiter) Fail(ip, identifier string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)

	for _, key := range keys(ip, identifier) {
		entry := l.buckets[key]
		if entry == nil {
			entry = &bucket{}
			l.buckets[key] = entry
		}
		entry.failures++
		entry.lastFailure = now
		if entry.failures > freeAttempts {
			entry.blockedUntil = now.Add(backoff(entry.failures - freeAttempts))
		}
	}
}

// Reset clears the counters after a successful login, so a person who
// eventually remembers their password is not still serving a penalty.
func (l *Limiter) Reset(ip, identifier string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys(ip, identifier) {
		delete(l.buckets, key)
	}
}

// 1s, 2s, 4s, 8s … capped. Doubling is what makes an online guessing attack
// uneconomic within a few dozen tries while staying invisible to a typo.
func backoff(step int) time.Duration {
	wait := time.Second << min(step-1, 16)
	if wait > maxBackoff || wait <= 0 {
		return maxBackoff
	}
	return wait
}

func (l *Limiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSwept) < sweepInterval {
		return
	}
	l.lastSwept = now
	for key, entry := range l.buckets {
		if now.Sub(entry.lastFailure) > bucketTTL && now.After(entry.blockedUntil) {
			delete(l.buckets, key)
		}
	}
}

func keys(ip, identifier string) []string {
	out := make([]string, 0, 2)
	if ip != "" {
		out = append(out, "ip:"+ip)
	}
	if trimmed := strings.ToLower(strings.TrimSpace(identifier)); trimmed != "" {
		out = append(out, "id:"+trimmed)
	}
	return out
}
