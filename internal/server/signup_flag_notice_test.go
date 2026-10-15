package server

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
)

// Refusals that land together are one burst as well. Each one reads the window
// before another has pushed into it, so without the instance lock around the
// check and the push every refusal adds its own entry. SQLite admits one writer
// at a time, so the burst cannot interleave there and the lock is never tested;
// the PostgreSQL run is the one that fails when the lock is taken out.
func TestConcurrentFlaggedSignupsAreOneAdministratorNotice(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		burstOfFlaggedSignups(t, newInstance(t))
	})
	t.Run("postgres is where the race is", func(t *testing.T) {
		burstOfFlaggedSignups(t, newInstance(t, func(c *config.Config) {
			c.Database = dbtest.Postgres(t, "the SQLite run above cannot fail this")
		}))
	})
}

// burstOfFlaggedSignups refuses sign-ups at once and counts the notices left.
// The instance must have been built with the subtest's t, so its helpers
// report to the right test.
func burstOfFlaggedSignups(t *testing.T, in *instance) {
	t.Helper()
	ctx := context.Background()
	review := auth.SignupReview{Ran: true, Decision: auth.SignupRefuse, Reason: "automated"}

	const bots = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range bots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			in.server.auth.OnSignupReview(ctx,
				auth.RegisterInput{Username: fmt.Sprintf("bot%02d", i), IP: "203.0.113.9"}, nil, review)
		}()
	}
	close(start)
	wg.Wait()

	if got := in.flaggedSignupNotices(ctx); got != 1 {
		t.Fatalf("%d refused sign-ups at once left %d administrator notices, want 1", bots, got)
	}
}

// A bot wave is refused one sign-up after another. Each refusal used to push
// its own administrator notice, so the bell filled with an entry per bot. A
// burst now shows as one entry; the security log still holds every refusal.
func TestABurstOfFlaggedSignupsIsOneAdministratorNotice(t *testing.T) {
	in := newInstance(t)
	ctx := context.Background()
	review := auth.SignupReview{Ran: true, Decision: auth.SignupRefuse, Reason: "automated"}

	for i := range 20 {
		in.server.auth.OnSignupReview(ctx,
			auth.RegisterInput{Username: fmt.Sprintf("bot%02d", i), IP: "203.0.113.9"}, nil, review)
	}

	if got := in.flaggedSignupNotices(ctx); got != 1 {
		t.Fatalf("20 refused sign-ups left %d administrator notices, want 1", got)
	}
}

// The window is for a burst, not for the rest of the day: a flagged sign-up
// after the previous notice has aged out gets its own entry.
func TestAFlaggedSignupAfterTheWindowGetsItsOwnNotice(t *testing.T) {
	in := newInstance(t)
	ctx := context.Background()
	review := auth.SignupReview{Ran: true, Decision: auth.SignupRestrict, Reason: "automated"}

	aged := time.Now().Add(-11 * time.Minute).UnixMilli()
	if err := in.server.notify.Push(ctx, nil, notify.Notification{
		Audience: notify.AudienceAdmins, Permission: "security",
		Kind: "signup_flagged", Params: map[string]any{"username": "earlier"},
		CreatedAt: aged,
	}); err != nil {
		t.Fatal(err)
	}

	in.server.auth.OnSignupReview(ctx,
		auth.RegisterInput{Username: "later", IP: "203.0.113.9"}, nil, review)

	if got := in.flaggedSignupNotices(ctx); got != 2 {
		t.Fatalf("a flagged sign-up eleven minutes after the last notice left %d notices, want 2", got)
	}
}

func (in *instance) flaggedSignupNotices(ctx context.Context) int {
	in.t.Helper()
	var count int
	if err := in.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE kind = ? AND audience = 'admins'`,
		"signup_flagged").Scan(&count); err != nil {
		in.t.Fatalf("count flagged-signup notices: %v", err)
	}
	return count
}
