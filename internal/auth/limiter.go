package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
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
	// Most buckets the map may hold. A field rather than the constant so a test
	// can make the table small; production never changes it.
	ceiling int
	// When the map was last walked because it was full. Zero until the first
	// walk, so a map that fills for the first time is walked at once.
	lastFullSweep time.Time
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
	// The same for one address, which is many people at an office or a
	// school behind one NAT. A success no longer clears it, so it needs the
	// room for a building's worth of typos; it is still a wall to a host
	// trying one guess at each of a thousand accounts.
	addressFreeAttempts = 30
	// How long a bucket survives with no activity.
	bucketTTL = 30 * time.Minute
	// Ceiling on the exponential backoff.
	maxBackoff = 15 * time.Minute
	// How often the map is swept for dead entries. Bounded work, done on the
	// write path, so there is no timer goroutine for this.
	sweepInterval = 5 * time.Minute
	// Most buckets the map may hold. Only failures and attempts in progress keep
	// a bucket, so sign-in alone never gets near this; what it bounds is a flood
	// of distinct names, which would otherwise grow the map for as long as the
	// flood lasted.
	maxBuckets = 100_000
	// How soon a full map may be walked again. While it stays full, every new
	// name would otherwise pay for a walk of the whole map under the lock.
	fullSweepInterval = time.Second
)

func NewLimiter() *Limiter {
	return &Limiter{buckets: map[string]*bucket{}, lastSwept: time.Now(), ceiling: maxBuckets}
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
				if free := allowance(key); entry.failures > free {
					entry.blockedUntil = now.Add(backoff(entry.failures - free))
				}
			case attemptSucceeded:
				// Only the account's own bucket is forgiven. The address bucket
				// is what stops one host trying many accounts, and a success
				// proves nothing about the other names it was guessing at:
				// clearing it let an attacker interleave one login to an account
				// of their own between every few wrong guesses and never be
				// slowed. Its failures age out with time like any other.
				//
				// Attempts that are still running keep the bucket alive and will
				// record their own result afterwards.
				if strings.HasPrefix(key, accountKeyPrefix) {
					entry.failures = 0
					entry.blockedUntil = time.Time{}
				}
			}

			if entry.inFlight == 0 && entry.failures == 0 {
				delete(a.limiter.buckets, key)
			}
		}
	})
}

// Attempt is what Begin hands back.
//
// Named, with exported outcomes, because signing in is not the only place a
// caller types a secret that a wrong answer has to make slower: a redemption
// code is guessed exactly the same way, and a second limiter elsewhere would
// be a second opinion about what "too many" means. Succeeded stays unexported
// on purpose — a caller that guesses at codes should never be able to clear
// its own failure count by eventually getting one right.
type Attempt = loginAttempt

func (a *loginAttempt) Failed()    { a.finish(attemptFailed) }
func (a *loginAttempt) Cancelled() { a.finish(attemptCancelled) }

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
		if entry.failures+entry.inFlight > allowance(key) {
			return nil, &RateLimitError{RetryAfter: time.Second}
		}
	}

	// Refused rather than evicted: a bucket holding a failure or a block is
	// what slows a guessed-at name, and freeing one to admit a stranger would
	// let a flood of new names reset that name's budget.
	if !l.roomFor(attemptKeys, now) {
		return nil, &RateLimitError{RetryAfter: time.Second}
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

// roomFor reports whether the keys this attempt would create fit under the
// ceiling, making room first when they do not. An attempt that creates no key
// needs no room, so a full map still serves every name it already holds.
func (l *Limiter) roomFor(attemptKeys []string, now time.Time) bool {
	if fresh := l.freshKeys(attemptKeys); fresh == 0 || len(l.buckets)+fresh <= l.ceiling {
		return true
	}
	// A walk costs the whole map under the lock, and a full map that stays full
	// would pay that for every new name. Most of what a walk frees is freed by
	// time rather than by the next request, so walking again a moment later
	// rarely finds more.
	if now.Sub(l.lastFullSweep) >= fullSweepInterval {
		l.lastFullSweep = now
		l.dropIdleLocked(now)
	}
	// The walk may have dropped a key this attempt already found. That key is
	// one the attempt must now create, so the count is taken again after it.
	return len(l.buckets)+l.freshKeys(attemptKeys) <= l.ceiling
}

// freshKeys counts the keys of an attempt that the map does not hold yet.
func (l *Limiter) freshKeys(attemptKeys []string) int {
	fresh := 0
	for _, key := range attemptKeys {
		if l.buckets[key] == nil {
			fresh++
		}
	}
	return fresh
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
	l.dropIdleLocked(now)
}

// dropIdleLocked removes every bucket that forgettable reports as idle. The
// periodic sweep and the full-map walk share it, so the two cannot disagree
// about what is safe to drop.
func (l *Limiter) dropIdleLocked(now time.Time) {
	for key, entry := range l.buckets {
		if forgettable(entry, now) {
			delete(l.buckets, key)
		}
	}
}

// forgettable reports whether a bucket is only memory now: it has been idle
// for bucketTTL, no block is still running, and no attempt is waiting to record
// an outcome. The last matters because finish skips a bucket it cannot find, so
// dropping one in flight would lose that outcome.
func forgettable(entry *bucket, now time.Time) bool {
	return entry.inFlight == 0 && now.Sub(entry.lastFailure) > bucketTTL && now.After(entry.blockedUntil)
}

const (
	accountKeyPrefix = "id:"
	// Longest identifier kept verbatim in a key. A username or e-mail address
	// is far shorter; anything longer is somebody filling the map, so it is
	// reduced to a digest and costs the same as any other key.
	maxKeyIdentifier = 256
)

// allowance is how many failures a bucket absorbs before it slows anybody.
func allowance(key string) int {
	if strings.HasPrefix(key, accountKeyPrefix) {
		return freeAttempts
	}
	return addressFreeAttempts
}

func keys(ip, identifier string) []string {
	out := make([]string, 0, 2)
	if ip != "" {
		// A whole IPv6 /64 is one subscriber, so it is one bucket.
		out = append(out, "ip:"+httpx.RateKey(ip))
	}
	if trimmed := strings.ToLower(strings.TrimSpace(identifier)); trimmed != "" {
		if len(trimmed) > maxKeyIdentifier {
			sum := sha256.Sum256([]byte(trimmed))
			trimmed = "#" + hex.EncodeToString(sum[:])
		}
		out = append(out, accountKeyPrefix+trimmed)
	}
	return out
}
