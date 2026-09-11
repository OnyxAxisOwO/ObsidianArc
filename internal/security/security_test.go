package security

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type fixture struct {
	store   *Store
	account user.User
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "security.db"),
		MaxOpenConns: 8, MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	groups := group.NewStore(db)
	defaultGroup, err := groups.Create(ctx, nil, group.CreateInput{
		Name: "Default", IsDefault: true, AllowAllModels: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := user.NewStore(db).Create(ctx, nil, user.CreateInput{
		Username: "reader", PasswordHash: "x", GroupID: defaultGroup.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{store: NewStore(db), account: account}
}

func TestSecurityEventsCanBeReadBackAndFiltered(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, event := range []Event{
		{Event: EventSignupReview, Severity: SeverityWarning, UserID: f.account.ID,
			Username: f.account.Username, Decision: "restrict", Reason: "suspicious"},
		{Event: EventChatChallenge, Severity: SeverityInfo, UserID: f.account.ID,
			Username: f.account.Username, Decision: "passed"},
	} {
		if err := f.store.Record(ctx, nil, event); err != nil {
			t.Fatal(err)
		}
	}

	events, total, err := f.store.List(ctx, Filter{Decision: "restrict", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(events) != 1 {
		t.Fatalf("total = %d, events = %d, want 1", total, len(events))
	}
	if events[0].Reason != "suspicious" || events[0].Username != "reader" {
		t.Fatalf("event = %+v", events[0])
	}
}

// The account row is the cross-process lock. Starting all attempts together
// reproduces the race that a process-local counter would lose: exactly one
// request owns the free place, however many connections observed it at once.
func TestParallelChatAttemptsCannotRacePastTheThreshold(t *testing.T) {
	f := newFixture(t)
	const attempts = 8
	start := make(chan struct{})
	results := make(chan ChatAttemptState, attempts)
	errorsByAttempt := make(chan error, attempts)
	var workers sync.WaitGroup
	now := time.Now()

	for range attempts {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			state, err := f.store.ChatAttempt(
				context.Background(), f.account.ID, now, 1, time.Minute)
			results <- state
			errorsByAttempt <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errorsByAttempt)

	for err := range errorsByAttempt {
		if err != nil {
			t.Fatal(err)
		}
	}
	allowed := 0
	for state := range results {
		if !state.Required {
			allowed++
		}
	}
	if allowed != 1 {
		t.Fatalf("%d attempts passed without a challenge, want 1", allowed)
	}
}

func TestSuccessfulChatChallengeGrantsTemporaryClearance(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now()
	if state, err := f.store.ChatAttempt(ctx, f.account.ID, now, 1, time.Minute); err != nil || state.Required {
		t.Fatalf("first attempt = %+v, err = %v", state, err)
	}
	if state, err := f.store.ChatAttempt(ctx, f.account.ID, now, 1, time.Minute); err != nil || !state.Required {
		t.Fatalf("second attempt = %+v, err = %v", state, err)
	}
	if err := f.store.ClearChatChallenge(ctx, f.account.ID, now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if state, err := f.store.ChatAttempt(ctx, f.account.ID, now.Add(time.Minute), 1, time.Minute); err != nil || state.Required {
		t.Fatalf("cleared attempt = %+v, err = %v", state, err)
	}
	if state, err := f.store.ChatAttempt(ctx, f.account.ID, now.Add(31*time.Minute), 1, time.Minute); err != nil || state.Required {
		t.Fatalf("first attempt after clearance = %+v, err = %v", state, err)
	}
}
