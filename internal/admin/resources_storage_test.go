package admin

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// storageHandlers returns handlers whose storage scan cannot start until
// release is called: the pool's only connection is held by a transaction, so
// the scan waits for it the way it would behind a slow one.
func storageHandlers(t *testing.T) (h *Handlers, release func()) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "storage.db"),
		MaxOpenConns: 1, MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	held, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release = func() {
		if !released {
			released = true
			_ = held.Rollback()
		}
	}
	t.Cleanup(release)
	return &Handlers{db: db, conversations: conversation.NewStore(db)}, release
}

type storageResult struct {
	totals storageTotals
	err    error
}

func callStorage(h *Handlers, ctx context.Context) <-chan storageResult {
	out := make(chan storageResult, 1)
	go func() {
		totals, err := h.storage(ctx)
		out <- storageResult{totals, err}
	}()
	return out
}

// A request waiting on another's scan used to sit on a mutex that ignores its
// context, holding its goroutine after its browser had gone. It leaves now,
// and the scan it was waiting on carries on for whoever is still there.
func TestAStorageWaiterLeavesWhenItsRequestIsCancelled(t *testing.T) {
	h, release := storageHandlers(t)

	first := callStorage(h, context.Background())
	time.Sleep(50 * time.Millisecond)
	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiter := callStorage(h, waiterCtx)
	time.Sleep(50 * time.Millisecond)
	cancelWaiter()

	select {
	case got := <-waiter:
		if !errors.Is(got.err, context.Canceled) {
			t.Errorf("the cancelled waiter returned %v, want context.Canceled", got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the cancelled waiter was still waiting on the scan")
	}

	release()
	select {
	case got := <-first:
		if got.err != nil {
			t.Fatalf("the scan failed: %v", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the scan never finished")
	}
}

// The scan is shared, so it cannot belong to the request that happened to
// start it: bound to that request's context, closing its tab failed the scan
// for every request waiting on it.
func TestTheSharedStorageScanOutlivesTheRequestThatStartedIt(t *testing.T) {
	h, release := storageHandlers(t)

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leader := callStorage(h, leaderCtx)
	time.Sleep(50 * time.Millisecond)
	follower := callStorage(h, context.Background())
	time.Sleep(50 * time.Millisecond)
	cancelLeader()

	select {
	case got := <-leader:
		if !errors.Is(got.err, context.Canceled) {
			t.Errorf("the cancelled leader returned %v, want context.Canceled", got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the cancelled leader was still waiting on the scan")
	}

	release()
	select {
	case got := <-follower:
		if got.err != nil {
			t.Fatalf("the follower got %v: the leader's cancellation reached the shared scan", got.err)
		}
		if got.totals.at.IsZero() {
			t.Error("the follower got no totals")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the follower never got an answer")
	}
	if _, cached := h.cachedStorage(); !cached {
		t.Error("the shared scan's answer was not cached")
	}
}
