package auth

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestActivityDoesNotWaitForSessionRenewal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "active", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-56 * time.Minute).UnixMilli()
	if _, err := f.db.Exec(ctx, `UPDATE users SET last_login_at = ?, last_active_at = ? WHERE id = ?`, old, old, account.ID); err != nil {
		t.Fatal(err)
	}
	before, _, err := f.auth.sessions.GetWithUser(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	active, after, err := f.auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if active.LastActiveAt < now || active.LastLoginAt != old {
		t.Fatalf("activity/login: %d/%d", active.LastActiveAt, active.LastLoginAt)
	}
	if after.LastSeenAt != before.LastSeenAt {
		t.Fatal("activity unexpectedly required renewing the session")
	}
	stored, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil || stored.LastActiveAt != active.LastActiveAt {
		t.Fatalf("activity not persisted: %+v %v", stored, err)
	}
	again, _, err := f.auth.Authenticate(ctx, token)
	if err != nil || again.LastActiveAt != active.LastActiveAt {
		t.Fatalf("activity writes are not throttled: %+v %v", again, err)
	}
}

func TestConcurrentActivityNeverMovesBackwards(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, _, err := f.auth.Register(ctx, RegisterInput{Username: "active", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errors := make(chan error, 16)
	var workers sync.WaitGroup
	for i := 1; i <= 16; i++ {
		workers.Add(1)
		go func(at int64) { defer workers.Done(); <-start; errors <- f.users.MarkActive(ctx, account.ID, at) }(int64(i))
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	stored, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil || stored.LastActiveAt != 16 {
		t.Fatalf("concurrent activity: %+v %v", stored, err)
	}
}

// Recording activity is bookkeeping. A write that cannot land must not turn
// a valid session into an anonymous request — the reader would see a 401
// for their own chat because a timestamp failed.
func TestAFailedActivityWriteStillAuthenticates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "active", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := f.db.Exec(ctx, `UPDATE users SET last_active_at = ? WHERE id = ?`, old, account.ID); err != nil {
		t.Fatal(err)
	}
	// SQLite's own way to make exactly that write fail.
	if _, err := f.db.Exec(ctx, `CREATE TRIGGER refuse_activity BEFORE UPDATE OF last_active_at ON users
		BEGIN SELECT RAISE(ABORT, 'activity refused'); END`); err != nil {
		t.Fatal(err)
	}

	active, _, err := f.auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("a failed activity write failed the sign-in: %v", err)
	}
	if active.ID != account.ID || active.LastActiveAt != old {
		t.Fatalf("authenticated %q with last_active_at %d, want %q and the stored %d",
			active.ID, active.LastActiveAt, account.ID, old)
	}
}
