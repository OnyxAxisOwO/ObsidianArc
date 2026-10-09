package auth

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func failAttempt(t *testing.T, l *Limiter, ip, identifier string) {
	t.Helper()
	attempt, err := l.Begin(ip, identifier)
	if err != nil {
		t.Fatalf("Begin(%q, %q): %v", ip, identifier, err)
	}
	attempt.finish(attemptFailed)
}

func isLimited(err error) bool {
	var limited *RateLimitError
	return errors.As(err, &limited)
}

// An attacker who owns one account can log in to it between wrong guesses at
// other people's. If that success cleared the address bucket the spray would
// never be slowed down.
func TestASuccessfulLoginDoesNotForgiveTheAddress(t *testing.T) {
	l := NewLimiter()
	const ip = "203.0.113.5"

	for i := 0; i < addressFreeAttempts+1; i++ {
		failAttempt(t, l, ip, fmt.Sprintf("victim-%d", i))
		if i < addressFreeAttempts {
			attempt, err := l.Begin(ip, "attacker")
			if err != nil {
				t.Fatalf("round %d: the attacker's own login was refused: %v", i, err)
			}
			attempt.finish(attemptSucceeded)
		}
	}

	if attempt, err := l.Begin(ip, "victim-next"); err == nil {
		attempt.finish(attemptCancelled)
		t.Fatal("interleaved successes kept the address from being limited")
	} else if !isLimited(err) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestASuccessfulLoginStillForgivesTheAccount(t *testing.T) {
	l := NewLimiter()

	for i := 0; i < 3; i++ {
		failAttempt(t, l, fmt.Sprintf("203.0.113.%d", i+1), "arc")
	}
	attempt, err := l.Begin("203.0.113.50", "arc")
	if err != nil {
		t.Fatal(err)
	}
	attempt.finish(attemptSucceeded)

	if _, ok := l.buckets["id:arc"]; ok {
		t.Error("the account's failures survived a successful login")
	}
	// The address it came from had no failures of its own and is gone too.
	if _, ok := l.buckets["ip:203.0.113.50"]; ok {
		t.Error("a clean address bucket was left behind")
	}
}

// A failure keeps the address bucket for the success to find, and the bucket
// still ends when nothing is left in it.
func TestSuccessLeavesTheAddressesFailuresInPlace(t *testing.T) {
	l := NewLimiter()
	failAttempt(t, l, "203.0.113.5", "someone")
	attempt, err := l.Begin("203.0.113.5", "arc")
	if err != nil {
		t.Fatal(err)
	}
	attempt.finish(attemptSucceeded)

	entry := l.buckets["ip:203.0.113.5"]
	if entry == nil || entry.failures != 1 {
		t.Fatalf("address bucket after a success = %+v, want its one failure kept", entry)
	}
}

func TestHugeIdentifiersDoNotBecomeHugeKeys(t *testing.T) {
	l := NewLimiter()
	huge := strings.Repeat("A", 1<<20)

	attempt, err := l.Begin("203.0.113.5", huge)
	if err != nil {
		t.Fatal(err)
	}
	attempt.finish(attemptFailed)

	for key := range l.buckets {
		if len(key) > maxKeyIdentifier+16 {
			t.Fatalf("a %d-byte key was stored", len(key))
		}
	}

	// Same name, any case, is still the same account...
	attempt, err = l.Begin("203.0.113.6", strings.ToLower(huge))
	if err != nil {
		t.Fatal(err)
	}
	attempt.finish(attemptFailed)
	count := 0
	for key, entry := range l.buckets {
		if strings.HasPrefix(key, accountKeyPrefix) {
			count++
			if entry.failures != 2 {
				t.Errorf("account bucket has %d failures, want 2", entry.failures)
			}
		}
	}
	if count != 1 {
		t.Fatalf("%d account buckets, want 1", count)
	}

	// ...and a different one is not folded into it.
	failAttempt(t, l, "203.0.113.7", strings.Repeat("A", 1<<20)+"b")
	if got := len(keys("", strings.Repeat("a", 300))); got != 1 {
		t.Fatalf("keys = %d", got)
	}
	if keys("", strings.Repeat("a", 300))[0] == keys("", strings.Repeat("a", 301))[0] {
		t.Error("two different long identifiers share a key")
	}
}

// Every address in an IPv6 /64 belongs to one subscriber, so rotating through
// them must not mint a fresh set of free attempts each time.
func TestIPv6AddressesInOneSubnetShareABucket(t *testing.T) {
	l := NewLimiter()

	for i := 0; i < addressFreeAttempts+1; i++ {
		failAttempt(t, l, fmt.Sprintf("2001:db8:0:1::%x", i+1), fmt.Sprintf("victim-%d", i))
	}
	if attempt, err := l.Begin("2001:db8:0:1:ffff::9", "victim-next"); err == nil {
		attempt.finish(attemptCancelled)
		t.Fatal("rotating through one /64 got fresh attempts every time")
	}
	// A neighbour in another /64 is somebody else.
	attempt, err := l.Begin("2001:db8:0:2::1", "victim-next")
	if err != nil {
		t.Fatalf("a different /64 was limited: %v", err)
	}
	attempt.finish(attemptCancelled)
}

// Every failed name keeps its bucket for half an hour, so a flood of failed
// names is what the ceiling has to stop.
func TestNewNamesStopAtTheCeiling(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 8

	for i := 0; i < 200; i++ {
		attempt, err := l.Begin("", fmt.Sprintf("name-%d", i))
		if err == nil {
			attempt.finish(attemptFailed)
		} else if !isLimited(err) {
			t.Fatalf("Begin: unexpected error %v", err)
		}
		if n := len(l.buckets); n > 8 {
			t.Fatalf("after %d names the map holds %d buckets, want at most 8", i+1, n)
		}
	}
	if n := len(l.buckets); n != 8 {
		t.Fatalf("the map holds %d buckets, want the ceiling of 8", n)
	}
}

// An attempt needs a bucket for each key it creates. When the map has room
// for only one of an address and an account, the whole attempt is refused:
// storing the address alone would leave a bucket whose in-flight count never
// comes back down, so the sweep could never remove it.
func TestARefusedAttemptLeavesNoKeysBehind(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 4
	failAttempt(t, l, "203.0.113.1", "one")
	failAttempt(t, l, "203.0.113.2", "two")

	if _, err := l.Begin("203.0.113.3", "three"); !isLimited(err) {
		t.Fatalf("a new address and a new account with the map full: err = %v, want rate limited", err)
	}
	if _, ok := l.buckets["ip:203.0.113.3"]; ok {
		t.Error("the address of a refused attempt was stored")
	}
	if n := len(l.buckets); n != 4 {
		t.Fatalf("the map holds %d buckets, want the 4 it had", n)
	}
}

// Once a bucket has been idle past its window it is only memory. A full map
// reclaims those before it turns anyone away, so a quiet map does not refuse
// a new name.
func TestAFullMapReclaimsIdleBucketsBeforeRefusing(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 4
	for i := 0; i < 4; i++ {
		failAttempt(t, l, "", fmt.Sprintf("old-%d", i))
	}
	idle := time.Now().Add(-2 * bucketTTL)
	for _, entry := range l.buckets {
		entry.lastFailure = idle
	}

	attempt, err := l.Begin("", "fresh")
	if err != nil {
		t.Fatalf("a full map of idle buckets refused a new name: %v", err)
	}
	attempt.finish(attemptFailed)

	if n := len(l.buckets); n != 1 {
		t.Fatalf("the map holds %d buckets, want only the new name's", n)
	}
}

// What a full map may not reclaim is anything that still counts: a failure
// inside its window, a block still running, or an attempt not yet finished.
// Each is set up with an idle window, so only the one under test protects it.
// The new name is refused, and every name already being guessed at keeps its
// budget.
func TestAFullMapNeverReclaimsABucketThatStillCounts(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 3
	idle := time.Now().Add(-2 * bucketTTL)

	failAttempt(t, l, "", "in-window")

	failAttempt(t, l, "", "blocked")
	blocked := l.buckets["id:blocked"]
	blocked.failures = freeAttempts + 1
	blocked.blockedUntil = time.Now().Add(time.Hour)
	blocked.lastFailure = idle

	busy, err := l.Begin("", "busy")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.finish(attemptCancelled)
	l.buckets["id:busy"].lastFailure = idle

	if _, err := l.Begin("", "newcomer"); !isLimited(err) {
		t.Fatalf("a full map of live buckets took a new name: err = %v", err)
	}
	for _, key := range []string{"id:in-window", "id:blocked", "id:busy"} {
		if _, ok := l.buckets[key]; !ok {
			t.Errorf("%s was reclaimed from a full map", key)
		}
	}
}

// A full map is walked at most once per interval. Without that, a flood of new
// names would walk the whole map once for each of them.
func TestAFullMapIsWalkedAtMostOncePerInterval(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 2
	failAttempt(t, l, "", "a")
	failAttempt(t, l, "", "b")

	// This walk runs, finds both names inside their window, and stamps itself.
	if _, err := l.Begin("", "c"); !isLimited(err) {
		t.Fatalf("a full map admitted a new name: err = %v", err)
	}
	l.buckets["id:a"].lastFailure = time.Now().Add(-2 * bucketTTL)

	// A walk stamped an hour ahead keeps the interval shut for any run this test
	// can have, so the idle "a" must survive the refusal.
	l.lastFullSweep = time.Now().Add(time.Hour)
	if _, err := l.Begin("", "d"); !isLimited(err) {
		t.Fatalf("a second walk ran inside the interval: err = %v", err)
	}
	if _, ok := l.buckets["id:a"]; !ok {
		t.Fatal("the map was walked again inside the interval")
	}

	// Once the interval has passed the walk runs, reclaims "a", and admits "d".
	l.lastFullSweep = time.Now().Add(-2 * fullSweepInterval)
	attempt, err := l.Begin("", "d")
	if err != nil {
		t.Fatalf("a walk after the interval did not make room: %v", err)
	}
	attempt.finish(attemptFailed)
	if _, ok := l.buckets["id:a"]; ok {
		t.Error("the walk after the interval left an idle bucket behind")
	}
}

// Below the ceiling nothing is reclaimed early: an idle bucket stays until the
// periodic sweep takes it, as it did before the ceiling existed.
func TestBelowTheCeilingNothingIsReclaimedEarly(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 10
	for i := 0; i < 5; i++ {
		failAttempt(t, l, "", fmt.Sprintf("idle-%d", i))
	}
	idle := time.Now().Add(-2 * bucketTTL)
	for _, entry := range l.buckets {
		entry.lastFailure = idle
	}

	attempt, err := l.Begin("", "fresh")
	if err != nil {
		t.Fatalf("a map with room refused a new name: %v", err)
	}
	attempt.finish(attemptFailed)

	if n := len(l.buckets); n != 6 {
		t.Fatalf("the map holds %d buckets, want 6: nothing below the ceiling should be reclaimed", n)
	}
	if !l.lastFullSweep.IsZero() {
		t.Error("the map was walked below its ceiling")
	}
}

// The account being guessed at keeps its budget through a flood of new names.
// Evicting its bucket to make room would let an attacker reset the count by
// spraying identifiers, which is the attack the limiter exists to slow down.
func TestAFloodOfNewNamesDoesNotResetAGuessedAccount(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 16
	for i := 0; i < freeAttempts; i++ {
		failAttempt(t, l, "", "victim")
	}

	for i := 0; i < 2000; i++ {
		attempt, err := l.Begin("", fmt.Sprintf("spray-%d", i))
		if err == nil {
			attempt.finish(attemptFailed)
		} else if !isLimited(err) {
			t.Fatalf("Begin: unexpected error %v", err)
		}
	}
	if n := len(l.buckets); n > 16 {
		t.Fatalf("the flood left %d buckets, above the ceiling of 16", n)
	}

	entry := l.buckets["id:victim"]
	if entry == nil || entry.failures != freeAttempts {
		t.Fatalf("the victim's bucket after the flood = %+v, want its %d failures kept", entry, freeAttempts)
	}
	// Its sixth wrong guess is the one past the allowance, and the account stays
	// refused after it.
	failAttempt(t, l, "", "victim")
	if _, err := l.Begin("", "victim"); !isLimited(err) {
		t.Fatalf("the guessed-at account was open again after the flood: err = %v", err)
	}
}

// Attempts from many goroutines at once must hold the ceiling at every instant,
// not only when they have all finished. The check and the insert share one
// lock, so two attempts cannot both see room for the last bucket.
func TestConcurrentAttemptsNeverExceedTheCeiling(t *testing.T) {
	const ceiling = 32
	l := NewLimiter()
	l.ceiling = ceiling

	var wg sync.WaitGroup
	// One overflow is the failure; reporting every later one only buries it.
	var reported sync.Once
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			ip := fmt.Sprintf("198.51.100.%d", g+1)
			for i := 0; i < 300; i++ {
				attempt, err := l.Begin(ip, fmt.Sprintf("g%d-%d", g, i))
				switch {
				case err != nil && !isLimited(err):
					t.Errorf("Begin: unexpected error %v", err)
				case err == nil && i%2 == 0:
					attempt.finish(attemptFailed)
				case err == nil:
					attempt.finish(attemptCancelled)
				}
				l.mu.Lock()
				n := len(l.buckets)
				l.mu.Unlock()
				if n > ceiling {
					reported.Do(func() {
						t.Errorf("the map holds %d buckets, above its ceiling of %d", n, ceiling)
					})
				}
			}
		}(g)
	}
	wg.Wait()
}

// A walk can drop a bucket that this attempt has already found, and then that
// key has to be created after all. Counting the new keys before the walk would
// admit the attempt one bucket past the ceiling.
func TestAWalkThatDropsAnAttemptsOwnKeyStillCountsIt(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 2
	failAttempt(t, l, "203.0.113.9", "")
	failAttempt(t, l, "", "other")
	l.buckets["ip:203.0.113.9"].lastFailure = time.Now().Add(-2 * bucketTTL)

	if _, err := l.Begin("203.0.113.9", "new"); !isLimited(err) {
		t.Fatalf("an attempt that needed two buckets with one free was admitted: err = %v", err)
	}
	if n := len(l.buckets); n > 2 {
		t.Fatalf("the map holds %d buckets, above its ceiling of 2", n)
	}
}
