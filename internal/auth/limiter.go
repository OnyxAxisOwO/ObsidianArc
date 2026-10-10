package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
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
//
// The map is bounded, and the bound is never paid for by a real account. A
// bucket for an identifier that named an account (bucket.matched) protects that
// account: it is never reclaimed, and it is not counted against the ceiling, so
// how many of them there are is bounded by the accounts the instance holds.
// Everything else — names that matched nothing, and addresses — is reclaimable
// unless it is in flight or still serving a block, and once those pass the
// ceiling the least recently used go. An attempt is not refused for want of
// room. Refusing it would turn a flood of invented names into a lockout for
// every real account whose first attempt came after the flood.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	// How many buckets protect an account. Kept as a count so the ceiling can be
	// checked without walking the map.
	protected int
	lastSwept time.Time
	// Most reclaimable buckets the map may hold. A field rather than the constant so a
	// test can make the table small; production never changes it.
	ceiling int
	// When the map was last walked because it was past the ceiling. Zero until
	// the first walk, so a map that fills for the first time is walked at once.
	lastFullSweep time.Time
	// Whether the last walk made all the room it was asked for. A walk that did
	// not is not repeated for every new name; see makeRoomFor.
	fullSweepMadeRoom bool
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
	// Set once an attempt against this identifier failed because it named an
	// account, and never cleared while the bucket lives. The key is the lowercased
	// identifier, so a spelling that matches nothing shares this bucket. If such a
	// spelling could clear the flag, one of them would make the account's bucket
	// reclaimable, and a flood would then reset its count.
	matched bool
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
	// Most reclaimable buckets the map may hold. What this bounds is a flood of
	// distinct names, which would otherwise grow the map for as long as the flood
	// lasted. Buckets that protect an account are not counted, so the map can be
	// larger than this by the number of accounts that have failed recently.
	maxBuckets = 100_000
	// How soon a full map may be walked again when the last walk could not make
	// room. Without it every new name would pay for a walk of the whole map under
	// the lock while nothing can be freed.
	fullSweepInterval = time.Second
)

func NewLimiter() *Limiter {
	return &Limiter{buckets: map[string]*bucket{}, lastSwept: time.Now(), ceiling: maxBuckets}
}

type attemptOutcome int

const (
	attemptCancelled attemptOutcome = iota
	attemptFailed
	// attemptFailedUnknown is a failure where the identifier named no account.
	// Its bucket protects nothing, so it can be reclaimed under pressure.
	attemptFailedUnknown
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
		l := a.limiter
		l.mu.Lock()
		defer l.mu.Unlock()

		for _, key := range a.keys {
			entry := l.buckets[key]
			if entry == nil {
				continue
			}
			if entry.inFlight > 0 {
				entry.inFlight--
			}

			switch outcome {
			case attemptFailed, attemptFailedUnknown:
				entry.failures++
				entry.lastFailure = now
				if free := allowance(key); entry.failures > free {
					entry.blockedUntil = now.Add(backoff(entry.failures - free))
				}
				if outcome == attemptFailed {
					l.markMatched(key, entry)
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
					l.markMatched(key, entry)
					entry.failures = 0
					entry.blockedUntil = time.Time{}
				}
			}

			if entry.inFlight == 0 && entry.failures == 0 {
				l.removeLocked(key)
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

	l.makeRoomFor(attemptKeys, now)

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

// makeRoomFor reclaims buckets when the reclaimable ones would pass the ceiling
// with this attempt's new keys added. It never refuses: whatever cannot be
// reclaimed (buckets in flight or still serving a block, the attempt's own keys,
// buckets that protect an account) is simply left in place, and the attempt goes
// ahead.
func (l *Limiter) makeRoomFor(attemptKeys []string, now time.Time) {
	if l.overCeiling(attemptKeys) <= 0 {
		return
	}
	// A walk costs the whole map under the lock. One that freed too little is not
	// repeated for every name that arrives, so it waits out fullSweepInterval.
	if !l.fullSweepMadeRoom && now.Sub(l.lastFullSweep) < fullSweepInterval {
		return
	}
	l.lastFullSweep = now
	l.dropIdleLocked(now)

	// The walk may have dropped a key this attempt already found, and that key is
	// one the attempt must now create, so the count is taken again after it.
	excess := l.overCeiling(attemptKeys)
	if excess <= 0 {
		l.fullSweepMadeRoom = true
		return
	}
	// Reclaiming a tenth more than the excess means the next walk is a tenth of
	// the ceiling's worth of names away, so the walk is paid for in batches.
	freed := l.evictLocked(attemptKeys, now, max(excess, l.ceiling/10))
	l.fullSweepMadeRoom = freed >= excess
}

// overCeiling is how far the reclaimable buckets run past the ceiling once the
// keys this attempt would create are counted. Negative or zero means room.
func (l *Limiter) overCeiling(attemptKeys []string) int {
	return len(l.buckets) - l.protected + l.freshKeys(attemptKeys) - l.ceiling
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

// evictLocked reclaims up to want buckets that protect nothing, and returns how
// many it reclaimed. The least recently used go first, and within that, names
// that matched nothing go before addresses. A flood of invented names then
// reclaims names rather than the blocks on the addresses sending it. Buckets
// in flight, buckets whose block is still running, and the attempt's own keys
// are never reclaimed. A running block is the slowing itself: reclaiming it
// would hand that address a fresh allowance in the middle of its wait.
func (l *Limiter) evictLocked(attemptKeys []string, now time.Time, want int) int {
	type candidate struct {
		key         string
		lastFailure time.Time
		address     bool
	}
	candidates := make([]candidate, 0)
	for key, entry := range l.buckets {
		if entry.matched || entry.inFlight > 0 || entry.blockedUntil.After(now) || slices.Contains(attemptKeys, key) {
			continue
		}
		candidates = append(candidates, candidate{
			key:         key,
			lastFailure: entry.lastFailure,
			address:     !strings.HasPrefix(key, accountKeyPrefix),
		})
	}
	slices.SortFunc(candidates, func(a, b candidate) int {
		if a.address != b.address {
			if a.address {
				return 1
			}
			return -1
		}
		return a.lastFailure.Compare(b.lastFailure)
	})

	n := min(want, len(candidates))
	for _, c := range candidates[:n] {
		l.removeLocked(c.key)
	}
	return n
}

// markMatched records that an account key named an account. It is one-way: see
// bucket.matched.
func (l *Limiter) markMatched(key string, entry *bucket) {
	if !strings.HasPrefix(key, accountKeyPrefix) || entry.matched {
		return
	}
	entry.matched = true
	l.protected++
}

// removeLocked drops a bucket and keeps the protected count in step with it.
func (l *Limiter) removeLocked(key string) {
	if entry := l.buckets[key]; entry != nil && entry.matched {
		l.protected--
	}
	delete(l.buckets, key)
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
			l.removeLocked(key)
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
