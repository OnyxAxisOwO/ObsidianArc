package server

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// twoInstancesOwingAPurge builds two servers on one database, the way the
// deployment notes allow, with a daily purge configured and last run before
// today's scheduled moment — so today's purge is owed.
func twoInstancesOwingAPurge(t *testing.T) (*Server, *Server) {
	t.Helper()
	in := newInstance(t)
	ctx := context.Background()
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if err := in.server.settings.SetMany(ctx, map[string]string{
		settings.AttachmentPurgeDaily: "00:00",
		settings.AttachmentPurgeLast:  strconv.FormatInt(midnight.Add(-time.Hour).UnixMilli(), 10),
	}); err != nil {
		t.Fatal(err)
	}
	// Built after the settings are written, so both caches start from the
	// same stored time and neither learns of the other's writes afterwards.
	other, err := New(ctx, in.server.deps)
	if err != nil {
		t.Fatalf("build the second instance: %v", err)
	}
	return in.server, other
}

// Two instances ticking together must not both run the day's purge: the
// second would empty whatever was uploaded since the first. Every claim races
// on real goroutines, and exactly one of them may come back as a run.
func TestOnlyOneInstanceClaimsTheDailyPurge(t *testing.T) {
	first, second := twoInstancesOwingAPurge(t)
	ctx := context.Background()

	const perInstance = 8
	decisions := make(chan conversation.Decision, 2*perInstance)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, s := range []*Server{first, second} {
		for range perInstance {
			wg.Add(1)
			go func(s *Server) {
				defer wg.Done()
				<-start
				claim, err := s.claimDailyPurge(ctx, "00:00", time.Now())
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				decisions <- claim.decision
			}(s)
		}
	}
	close(start)
	wg.Wait()
	close(decisions)

	runs := 0
	for decision := range decisions {
		if decision == conversation.DecisionRun {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("%d claims ran today's purge, want exactly 1", runs)
	}
}

// The instance that did not run the purge still holds the old time in its
// cache. Its next pass must read the database, not the cache, or it purges
// a second time.
func TestAStaleInstanceSkipsAPurgeAnotherHasRun(t *testing.T) {
	first, second := twoInstancesOwingAPurge(t)
	ctx := context.Background()

	first.sweepAttachments(ctx)
	if claim, err := second.claimDailyPurge(ctx, "00:00", time.Now()); err != nil {
		t.Fatal(err)
	} else if claim.decision != conversation.DecisionSkip {
		t.Fatalf("second instance decided %v after the first ran today's purge, want skip", claim.decision)
	}
}

// A purge that was claimed and then failed is owed again on the next tick,
// rather than recorded as done for the day.
func TestAReleasedPurgeClaimIsOwedAgain(t *testing.T) {
	first, _ := twoInstancesOwingAPurge(t)
	ctx := context.Background()

	claim, err := first.claimDailyPurge(ctx, "00:00", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if claim.decision != conversation.DecisionRun {
		t.Fatalf("first claim decided %v, want run", claim.decision)
	}
	first.releaseDailyPurge(ctx, claim)

	if got := first.settings.Get(settings.AttachmentPurgeLast); got != strconv.FormatInt(claim.previous, 10) {
		t.Fatalf("cache holds %q after the release, want %d", got, claim.previous)
	}
	again, err := first.claimDailyPurge(ctx, "00:00", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if again.decision != conversation.DecisionRun {
		t.Fatalf("claim after a release decided %v, want run", again.decision)
	}
}
