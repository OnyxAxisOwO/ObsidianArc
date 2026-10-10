package auth

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// failAttempt is a failure against a name that matched an account.
func failAttempt(t *testing.T, l *Limiter, ip, identifier string) {
	t.Helper()
	attempt, err := l.Begin(ip, identifier)
	if err != nil {
		t.Fatalf("Begin(%q, %q): %v", ip, identifier, err)
	}
	attempt.finish(attemptFailed)
}

// failUnknown is a failure against a name that matched no account.
func failUnknown(t *testing.T, l *Limiter, ip, identifier string) {
	t.Helper()
	attempt, err := l.Begin(ip, identifier)
	if err != nil {
		t.Fatalf("Begin(%q, %q): %v", ip, identifier, err)
	}
	attempt.finish(attemptFailedUnknown)
}

// reclaimable is what the ceiling bounds: every bucket that protects no account.
func reclaimable(l *Limiter) int {
	return len(l.buckets) - l.protected
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

// A name that matched nothing keeps its bucket for half an hour, so a flood of
// them is what the ceiling has to bound. The flood is never refused: the map
// holds no more reclaimable buckets than the ceiling allows.
func TestUnknownNamesAreReclaimedAtTheCeiling(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 8

	for i := 0; i < 200; i++ {
		attempt, err := l.Begin("", fmt.Sprintf("name-%d", i))
		if err != nil {
			t.Fatalf("name %d was refused: %v", i+1, err)
		}
		attempt.finish(attemptFailedUnknown)
		if n := reclaimable(l); n > 8 {
			t.Fatalf("after %d names the map holds %d reclaimable buckets, want at most 8", i+1, n)
		}
	}
	if n := len(l.buckets); n != 8 {
		t.Fatalf("the map holds %d buckets, want the ceiling of 8", n)
	}
}

// Under pressure, names that matched nothing are reclaimed before addresses, so
// a flood of invented names does not spend the blocks on the addresses sending
// it. Here the addresses are the older buckets, which is what a plain
// least-recently-used rule would take first.
func TestUnknownNamesAreReclaimedBeforeAddresses(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 4
	failUnknown(t, l, "203.0.113.1", "")
	failUnknown(t, l, "203.0.113.2", "")
	failUnknown(t, l, "", "one")
	failUnknown(t, l, "", "two")

	older := time.Now().Add(-10 * time.Minute)
	l.buckets["ip:203.0.113.1"].lastFailure = older
	l.buckets["ip:203.0.113.2"].lastFailure = older

	attempt, err := l.Begin("", "three")
	if err != nil {
		t.Fatalf("a new name was refused: %v", err)
	}
	attempt.finish(attemptCancelled)

	for _, key := range []string{"ip:203.0.113.1", "ip:203.0.113.2"} {
		if _, ok := l.buckets[key]; !ok {
			t.Errorf("%s was reclaimed while a name that matched nothing was available", key)
		}
	}
	if _, ok := l.buckets["id:one"]; ok {
		t.Error("the least recently used name was kept while it was the one to reclaim")
	}
}

// A refusal is never the answer to a full map. When nothing can be reclaimed,
// the attempt goes ahead, and the in-flight counts come back down once it
// finishes, so the buckets it left behind can be reclaimed later.
func TestAnAttemptIsNeverRefusedForWantOfRoom(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 2

	a, err := l.Begin("203.0.113.1", "one")
	if err != nil {
		t.Fatal(err)
	}
	b, err := l.Begin("203.0.113.2", "two")
	if err != nil {
		t.Fatal(err)
	}
	c, err := l.Begin("203.0.113.3", "three")
	if err != nil {
		t.Fatalf("a full map of attempts in flight refused a new one: %v", err)
	}
	for _, attempt := range []*loginAttempt{a, b, c} {
		attempt.finish(attemptFailedUnknown)
	}
	for key, entry := range l.buckets {
		if entry.inFlight != 0 {
			t.Errorf("%s still has %d attempts in flight after they finished", key, entry.inFlight)
		}
	}
}

// A walk must never take a bucket that protects an account or one with an
// attempt still in flight, even when it is the only thing in the map to take.
// The new name is admitted and both of those survive.
func TestAProtectedOrInFlightBucketIsNeverReclaimed(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 1
	failAttempt(t, l, "", "known")

	busy, err := l.Begin("", "busy")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.finish(attemptCancelled)

	attempt, err := l.Begin("", "newcomer")
	if err != nil {
		t.Fatalf("a full map refused a new name: %v", err)
	}
	attempt.finish(attemptCancelled)

	for _, key := range []string{"id:known", "id:busy"} {
		if _, ok := l.buckets[key]; !ok {
			t.Errorf("%s was reclaimed for room", key)
		}
	}
}

// A walk that could not free anything is not repeated for every name that
// arrives inside the interval: those names are admitted without a walk, so the
// bucket that became reclaimable meanwhile is still there. Once the interval has
// passed, the walk runs again and reclaims it.
func TestAWalkThatFreedNothingIsNotRepeatedWithinTheInterval(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 2

	a, err := l.Begin("", "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := l.Begin("", "b")
	if err != nil {
		t.Fatal(err)
	}
	// Both are in flight, so this walk has nothing it may take.
	c, err := l.Begin("", "c")
	if err != nil {
		t.Fatalf("a walk that freed nothing refused a name: %v", err)
	}
	a.finish(attemptFailedUnknown)

	d, err := l.Begin("", "d")
	if err != nil {
		t.Fatalf("a name inside the interval was refused: %v", err)
	}
	if _, ok := l.buckets["id:a"]; !ok {
		t.Fatal("the map was walked again inside the interval")
	}

	l.lastFullSweep = time.Now().Add(-2 * fullSweepInterval)
	e, err := l.Begin("", "e")
	if err != nil {
		t.Fatalf("a name after the interval was refused: %v", err)
	}
	if _, ok := l.buckets["id:a"]; ok {
		t.Error("the walk after the interval left a reclaimable bucket behind")
	}
	for _, attempt := range []*loginAttempt{b, c, d, e} {
		attempt.finish(attemptCancelled)
	}
}

// The attempt's own address is never reclaimed to make room for its own name,
// even when it is the least recently used bucket in the map. Reclaiming it would
// hand the attempt a fresh address budget in the middle of the attempt.
func TestAWalkNeverReclaimsTheAttemptsOwnKeys(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 2
	failUnknown(t, l, "203.0.113.9", "")
	failUnknown(t, l, "", "other")
	l.buckets["ip:203.0.113.9"].lastFailure = time.Now().Add(-time.Minute)

	attempt, err := l.Begin("203.0.113.9", "new")
	if err != nil {
		t.Fatalf("an attempt that needed room for one name was refused: %v", err)
	}
	attempt.finish(attemptCancelled)

	if _, ok := l.buckets["ip:203.0.113.9"]; !ok {
		t.Error("the attempt's own address was reclaimed for its own name")
	}
	if _, ok := l.buckets["id:other"]; ok {
		t.Error("the other name was kept while the room was needed")
	}
}

// Below the ceiling nothing is reclaimed early: an idle bucket stays until the
// periodic sweep takes it, as it did before the ceiling existed.
func TestBelowTheCeilingNothingIsReclaimedEarly(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 10
	for i := 0; i < 5; i++ {
		failUnknown(t, l, "", fmt.Sprintf("idle-%d", i))
	}
	idle := time.Now().Add(-2 * bucketTTL)
	for _, entry := range l.buckets {
		entry.lastFailure = idle
	}

	attempt, err := l.Begin("", "fresh")
	if err != nil {
		t.Fatalf("a map with room refused a new name: %v", err)
	}
	attempt.finish(attemptFailedUnknown)

	if n := len(l.buckets); n != 6 {
		t.Fatalf("the map holds %d buckets, want 6: nothing below the ceiling should be reclaimed", n)
	}
	if !l.lastFullSweep.IsZero() {
		t.Error("the map was walked below its ceiling")
	}
}

// Once a name's bucket has been idle past its window it is only memory. A full
// map of such names is swept for the new one, so the map holds only the new
// name afterwards.
func TestAFullMapOfIdleNamesIsSweptForANewName(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 4
	for i := 0; i < 4; i++ {
		failUnknown(t, l, "", fmt.Sprintf("old-%d", i))
	}
	idle := time.Now().Add(-2 * bucketTTL)
	for _, entry := range l.buckets {
		entry.lastFailure = idle
	}

	attempt, err := l.Begin("", "fresh")
	if err != nil {
		t.Fatalf("a full map of idle names refused a new name: %v", err)
	}
	attempt.finish(attemptFailedUnknown)

	if n := len(l.buckets); n != 1 {
		t.Fatalf("the map holds %d buckets, want only the new name's", n)
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
		if err != nil {
			t.Fatalf("spray %d was refused: %v", i+1, err)
		}
		attempt.finish(attemptFailedUnknown)
	}
	if n := reclaimable(l); n > 16 {
		t.Fatalf("the flood left %d reclaimable buckets, above the ceiling of 16", n)
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

// A real account that has never failed signs in during a flood of invented
// names, and the failures already recorded against another real account are
// still there when the flood is over.
func TestAFloodOfUnknownNamesNeverRefusesARealAccount(t *testing.T) {
	l := NewLimiter()
	l.ceiling = 16
	for i := 0; i < freeAttempts; i++ {
		failAttempt(t, l, "", "victim")
	}

	for i := 0; i < 2000; i++ {
		attempt, err := l.Begin("", fmt.Sprintf("invented-%d", i))
		if err != nil {
			t.Fatalf("an invented name was refused: %v", err)
		}
		attempt.finish(attemptFailedUnknown)
	}

	attempt, err := l.Begin("198.51.100.9", "bob")
	if err != nil {
		t.Fatalf("a real account that has never failed was refused during the flood: %v", err)
	}
	attempt.finish(attemptSucceeded)

	if entry := l.buckets["id:victim"]; entry == nil || entry.failures != freeAttempts {
		t.Fatalf("the victim's failures after the flood = %+v, want %d", entry, freeAttempts)
	}
}

// Only a name that matched an account is protected, so that is the only thing
// a failure can protect.
func TestOnlyANameThatMatchedAnAccountIsProtected(t *testing.T) {
	l := NewLimiter()
	failUnknown(t, l, "", "nobody")
	if l.protected != 0 {
		t.Fatalf("an invented name protected %d buckets", l.protected)
	}
	failAttempt(t, l, "", "arc")
	if l.protected != 1 {
		t.Fatalf("a name that matched an account protects %d buckets, want 1", l.protected)
	}
}

// A spelling of a real name that matches nothing shares its bucket, because the
// key is the lowercased identifier. It must not clear the protection, or a
// guesser could make the account reclaimable by sending one such spelling.
func TestAMatchedNameStaysProtectedWhenAVariantFailsUnknown(t *testing.T) {
	l := NewLimiter()
	failAttempt(t, l, "", "Arc")
	failUnknown(t, l, "", "  ARC ")

	entry := l.buckets["id:arc"]
	if entry == nil || !entry.matched {
		t.Fatalf("the account's bucket after a variant = %+v, want it still protected", entry)
	}
	if entry.failures != 2 || l.protected != 1 {
		t.Fatalf("failures %d, protected %d; want 2 and 1", entry.failures, l.protected)
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
				n := reclaimable(l)
				l.mu.Unlock()
				if n > ceiling {
					reported.Do(func() {
						t.Errorf("the map holds %d reclaimable buckets, above its ceiling of %d", n, ceiling)
					})
				}
			}
		}(g)
	}
	wg.Wait()

	matched := 0
	for _, entry := range l.buckets {
		if entry.matched {
			matched++
		}
	}
	if matched != l.protected {
		t.Errorf("the protected count is %d, but %d buckets are protected", l.protected, matched)
	}
}
