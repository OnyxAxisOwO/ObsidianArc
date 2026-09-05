package trial

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The trial spends the operator's provider credit for someone who has not
// identified themselves, so what stops it running away is worth testing
// directly rather than through the handler.

func TestBudgetAllowsUpToTheBurstThenRefuses(t *testing.T) {
	b := newBudget()

	for i := 0; i < burstPerAddress; i++ {
		if ok, _ := b.take("198.51.100.7"); !ok {
			t.Fatalf("refused turn %d of an allowed %d", i+1, burstPerAddress)
		}
	}

	ok, retryAfter := b.take("198.51.100.7")
	if ok {
		t.Fatal("allowed a turn past the burst")
	}
	if retryAfter <= 0 || retryAfter > budgetWindow {
		t.Errorf("retry after = %v, want something inside the window", retryAfter)
	}
}

func TestBudgetIsPerAddress(t *testing.T) {
	b := newBudget()

	for i := 0; i < burstPerAddress; i++ {
		b.take("198.51.100.7")
	}
	if ok, _ := b.take("203.0.113.9"); !ok {
		t.Fatal("one address exhausting its budget refused another")
	}
}

// An address the proxy configuration did not supply must not be a free pass.
// Counting them together is stricter than letting them through uncounted.
func TestBudgetCountsUnknownAddressesTogether(t *testing.T) {
	b := newBudget()

	for i := 0; i < burstPerAddress; i++ {
		if ok, _ := b.take(""); !ok {
			t.Fatalf("refused turn %d from an unknown address", i+1)
		}
	}
	if ok, _ := b.take("   "); ok {
		t.Fatal("blank and empty addresses were budgeted separately")
	}
}

func TestBudgetForgetsAnExpiredWindow(t *testing.T) {
	b := newBudget()

	for i := 0; i < burstPerAddress; i++ {
		b.take("198.51.100.7")
	}
	if ok, _ := b.take("198.51.100.7"); ok {
		t.Fatal("expected the address to be exhausted")
	}

	// Age the window past its length. Without this the limit would be a
	// lifetime cap and the front door would close itself permanently.
	b.windows["198.51.100.7"].startAt = time.Now().Add(-budgetWindow - time.Minute)

	if ok, _ := b.take("198.51.100.7"); !ok {
		t.Fatal("the window did not roll over")
	}
}

// --- the turn limit -----------------------------------------------------------

// Counting the turns in the request body was counting a number the client
// wrote: a visitor who sends only their latest question looks like turn one
// forever. The count is signed and handed back instead.

func TestContinuationRoundTrips(t *testing.T) {
	s := newSigner([]byte("an-instance-secret"))

	token := s.issue(3, time.Now())
	turns, err := s.verify(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if turns != 3 {
		t.Errorf("turns = %d, want 3", turns)
	}
}

func TestAbsentContinuationIsAFreshTrial(t *testing.T) {
	s := newSigner([]byte("an-instance-secret"))
	for _, token := range []string{"", "   "} {
		turns, err := s.verify(token)
		if err != nil || turns != 0 {
			t.Errorf("verify(%q) = %d, %v; want 0, nil", token, turns, err)
		}
	}
}

// The point of the whole mechanism: an edited count is not believed.
func TestEditedContinuationIsRefused(t *testing.T) {
	s := newSigner([]byte("an-instance-secret"))
	token := s.issue(9, time.Now())

	_, expiry, _ := strings.Cut(token, ".")
	forged := "0." + expiry
	if _, err := s.verify(forged); err == nil {
		t.Fatal("a rewritten turn count was accepted")
	}

	for _, bad := range []string{"garbage", "1.2", "1.2.3", token + "x", "x" + token} {
		if _, err := s.verify(bad); err == nil {
			t.Errorf("verify(%q) was accepted", bad)
		}
	}
}

// A token from a different instance, or from before the secret was rotated,
// is not a fresh trial — it is not believed at all.
func TestContinuationFromAnotherSecretIsRefused(t *testing.T) {
	mine := newSigner([]byte("my-secret"))
	theirs := newSigner([]byte("their-secret"))

	if _, err := mine.verify(theirs.issue(1, time.Now())); err == nil {
		t.Fatal("a token signed elsewhere was accepted")
	}
}

// Waiting for the token to expire must not be a way to start over.
func TestExpiredContinuationIsRefusedRatherThanReset(t *testing.T) {
	s := newSigner([]byte("an-instance-secret"))
	token := s.issue(3, time.Now().Add(-continuationTTL-time.Minute))

	turns, err := s.verify(token)
	if err == nil {
		t.Fatalf("an expired token was accepted as turn %d", turns)
	}
}

// --- the ceilings that hold when the token is thrown away ---------------------

func TestInstanceBudgetHoldsAcrossAddresses(t *testing.T) {
	b := newBudget()

	// Every request from a different address, which is what spoofing looks
	// like. The per-address budget never trips; the instance one does.
	spent := 0
	for i := 0; i < burstPerInstance+50; i++ {
		if ok, _ := b.take(fmt.Sprintf("198.51.100.%d", i%256)); ok {
			spent++
		}
	}
	if spent != burstPerInstance {
		t.Fatalf("%d turns were allowed across many addresses, want %d", spent, burstPerInstance)
	}
}

func TestConcurrencyCapBoundsOpenGenerations(t *testing.T) {
	b := newBudget()

	releases := make([]func(), 0, maxConcurrent)
	for i := 0; i < maxConcurrent; i++ {
		release, ok := b.enter()
		if !ok {
			t.Fatalf("slot %d of an allowed %d was refused", i+1, maxConcurrent)
		}
		releases = append(releases, release)
	}
	if _, ok := b.enter(); ok {
		t.Fatal("a generation past the concurrency cap was allowed")
	}

	releases[0]()
	releases[0]() // idempotent
	if _, ok := b.enter(); !ok {
		t.Fatal("a freed slot was not reusable")
	}
}

func TestTrialOutputIsCappedBelowTheModel(t *testing.T) {
	if got := trialMaxTokens(0); got != MaxTrialOutputTokens {
		t.Errorf("with no model ceiling: %d, want %d", got, MaxTrialOutputTokens)
	}
	if got := trialMaxTokens(100000); got != MaxTrialOutputTokens {
		t.Errorf("with a large model ceiling: %d, want %d", got, MaxTrialOutputTokens)
	}
	// A model that can produce less than the trial allows still wins.
	if got := trialMaxTokens(120); got != 120 {
		t.Errorf("with a smaller model ceiling: %d, want 120", got)
	}
}
