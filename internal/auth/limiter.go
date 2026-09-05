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
	// Attempts that passed the gate but have not finished password hashing.
	// Counting them closes the burst where many requests all call Allow
	// before any one of them has had time to call Fail.
	inFlight int
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

type attemptOutcome int

const (
	attemptCancelled attemptOutcome = iota
	attemptFailed
	attemptSucceeded
)

// loginAttempt is the reservation made before password hashing begins.
// finish is idempotent so an explicit outcome and a deferred cancellation can
// safely coexist on every return path through Login.
type loginAttempt struct {
	limiter *Limiter
	keys    []string
	once    sync.Once
}

func (a *loginAttempt) finish(outcome attemptOutcome) {
	if a == nil || a.limiter == nil {
		return
	}
	a.once.Do(func() {
		now := time.Now()
		a.limiter.mu.Lock()
		defer a.limiter.mu.Unlock()

		for _, key := range a.keys {
			entry := a.limiter.buckets[key]
			if entry == nil {
				continue
			}
			if entry.inFlight > 0 {
				entry.inFlight--
			}

			switch outcome {
			case attemptFailed:
				entry.failures++
				entry.lastFailure = now
				if entry.failures > freeAttempts {
					entry.blockedUntil = now.Add(backoff(entry.failures - freeAttempts))
				}
			case attemptSucceeded:
				// A successful credential clears old failures, but attempts that
				// are still running keep the bucket alive and will record their
				// own result afterwards.
				entry.failures = 0
				entry.blockedUntil = time.Time{}
			}

			if entry.inFlight == 0 && entry.failures == 0 {
				delete(a.limiter.buckets, key)
			}
		}
	})
}

// RateLimitError carries how long the caller must wait, so the handler can
// send a Retry-After the client can act on.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("too many attempts; try again in %s", e.RetryAfter.Round(time.Second))
}

// Begin reports whether an attempt may proceed and, when it may, reserves a
// place before the expensive password verification starts.
func (l *Limiter) Begin(ip, identifier string) (*loginAttempt, error) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)

	attemptKeys := keys(ip, identifier)
	for _, key := range attemptKeys {
		entry := l.buckets[key]
		if entry == nil {
			continue
		}
		if wait := entry.blockedUntil.Sub(now); wait > 0 {
			return nil, &RateLimitError{RetryAfter: wait}
		}
		// Sequential behaviour permits the sixth try and blocks after it
		// fails. Parallel requests get the same allowance, not an unlimited
		// wave that happened to arrive before the first hash completed.
		if entry.failures+entry.inFlight > freeAttempts {
			return nil, &RateLimitError{RetryAfter: time.Second}
		}
	}

	for _, key := range attemptKeys {
		entry := l.buckets[key]
		if entry == nil {
			entry = &bucket{}
			l.buckets[key] = entry
		}
		entry.inFlight++
		// Also serves as last activity for the sweeper while this attempt is
		// waiting for an Argon2 slot.
		entry.lastFailure = now
	}
	return &loginAttempt{limiter: l, keys: attemptKeys}, nil
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
		if entry.inFlight == 0 && now.Sub(entry.lastFailure) > bucketTTL && now.After(entry.blockedUntil) {
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
