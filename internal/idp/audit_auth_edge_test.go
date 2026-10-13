package idp

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// Creating an application waits for the instance lock, so the ceiling's count
// and insert cannot interleave with another create. Postgres only: SQLite
// already serialises writers.
func TestCreatingAnApplicationWaitsForTheInstanceLock(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, dbtest.Postgres(t,
		"the application ceiling must hold the instance lock across engines"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)

	locked := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unlock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unlock)
	holder := make(chan error, 1)
	go func() {
		holder <- db.Tx(ctx, func(tx *database.Tx) error {
			if err := settings.Lock(ctx, tx); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case err := <-holder:
		t.Fatalf("take the instance lock: %v", err)
	case <-time.After(5 * time.Second):
		unlock()
		t.Fatal("timed out taking the instance lock")
	}

	created := make(chan error, 1)
	go func() {
		_, _, err := store.CreateApp(ctx, CreateAppInput{
			Name:         "Audit",
			RedirectURIs: "https://app.example.com/callback",
			Public:       true,
		})
		created <- err
	}()
	select {
	case err := <-created:
		unlock()
		t.Fatalf("CreateApp finished while the instance lock was held (err %v)", err)
	case <-time.After(2 * time.Second):
	}

	unlock()
	if err := <-created; err != nil {
		t.Fatalf("CreateApp after the lock was released: %v", err)
	}
	if err := <-holder; err != nil {
		t.Fatalf("holder: %v", err)
	}
}
