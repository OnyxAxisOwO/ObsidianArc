package auth

import (
	"errors"
	"fmt"
	"strings"
	"testing"
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
