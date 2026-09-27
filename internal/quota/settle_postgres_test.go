package quota

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

func TestSettleDoesNotWaitForGlobalResetLock(t *testing.T) {
	db, err := database.Open(context.Background(), dbtest.Postgres(t,
		"the settlement path must not serialize unrelated completed turns on the global reset row"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	set := settings.New(db)
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := NewService(db, NewStore(db), set)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := service.ResetAll(ctx); err != nil {
		t.Fatal(err)
	}

	locked := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unlock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unlock)
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- db.Tx(ctx, func(tx *database.Tx) error {
			if _, err := tx.Exec(ctx,
				`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
				 ON CONFLICT (key) DO UPDATE SET updated_at = settings.updated_at`,
				globalResetKey, "0", time.Now().UnixMilli()); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case err := <-lockDone:
		t.Fatalf("lock global reset row: %v", err)
	case <-time.After(5 * time.Second):
		unlock()
		t.Fatal("timed out acquiring global reset row lock")
	}

	settled := make(chan error, 1)
	go func() {
		settled <- service.Settle(ctx, account("settling-user", ""), Estimate{}, Estimate{Tokens: 7})
	}()
	select {
	case err := <-settled:
		if err != nil {
			unlock()
			t.Fatalf("settle while reset row is locked: %v", err)
		}
	case <-time.After(2 * time.Second):
		unlock()
		<-lockDone
		t.Fatal("settlement waited for the global reset row lock")
	}

	unlock()
	if err := <-lockDone; err != nil {
		t.Fatalf("release global reset row lock: %v", err)
	}
	if err := service.ResetAll(ctx); err != nil {
		t.Fatalf("reset after settled turn: %v", err)
	}
	var counters int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM usage_counters`).Scan(&counters); err != nil {
		t.Fatal(err)
	}
	if counters != 0 {
		t.Errorf("global reset left %d quota counters, want none", counters)
	}
}
