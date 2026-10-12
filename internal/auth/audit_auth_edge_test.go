package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/pow"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

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
