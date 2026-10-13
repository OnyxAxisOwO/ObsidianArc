package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/pow"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// A solution refused for a stale tier is the browser's cue to fetch a new
// challenge, which the sign-up form does after any refusal. It must reach the
// browser as a client refusal the form can word, not as a server fault.
func TestAStaleTierSolutionIsAClientRefusalNotAServerFault(t *testing.T) {
	var h Handlers
	var httpErr *httpx.Error
	if err := h.registrationError(pow.ErrBelowTier); !errors.As(err, &httpErr) {
		t.Fatalf("registrationError(ErrBelowTier) = %v, want an httpx error", err)
	}
	if httpErr.Status != http.StatusBadRequest || httpErr.Code != "pow_expired" {
		t.Fatalf("registrationError(ErrBelowTier) = %d %q, want 400 pow_expired", httpErr.Status, httpErr.Code)
	}
}

// A challenge handed out at the base tier is refused once activity from the
// address has raised the difficulty, and the difficulty actually required is
// the elevated one.
func TestASolutionAtTheBaseTierIsRefusedOnceTheElevatedTierApplies(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.auth.PoW = pow.NewManager([]byte("a-tier-check-test-key"), pow.NewTracker())
	for key, value := range map[string]string{
		settings.PoWBaseMaxNumber:     "100",
		settings.PoWElevatedMaxNumber: "5000",
		settings.PoWThreshold:         "1",
	} {
		if err := f.settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	const ip = "203.0.113.9"

	early, err := f.auth.PoW.Issue(f.settings.PoWBaseMaxNumber())
	if err != nil {
		t.Fatal(err)
	}
	baseSolution := solveProofForTest(t, early)

	// One attempt from this address is enough to raise the tier at a threshold of one.
	f.auth.PoW.Tracker().RecordAttempt(ip, time.Now())
	if err := f.auth.CheckSignupProof(ctx, ip, baseSolution); !errors.Is(err, pow.ErrBelowTier) {
		t.Fatalf("base-tier solution after the tier rose = %v, want ErrBelowTier", err)
	}

	later, err := f.auth.PoW.Issue(f.settings.PoWElevatedMaxNumber())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.auth.CheckSignupProof(ctx, ip, solveProofForTest(t, later)); err != nil {
		t.Fatalf("elevated-tier solution = %v, want it accepted", err)
	}
}
