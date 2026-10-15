package user

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
)

func TestBehaviouralBooleanPreferenceIsStrict(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "preferences.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO users (id, username, username_lower, password_hash, role, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"reader", "reader", "reader", "x", RoleUser, StatusActive, 1, 1); err != nil {
		t.Fatal(err)
	}

	store := NewPreferenceStore(db)
	if enabled, err := store.Bool(ctx, "reader", AutoUseResetCard); err != nil || enabled {
		t.Fatalf("empty preference = %v, %v", enabled, err)
	}
	if _, err := store.Merge(ctx, "reader", map[string]json.RawMessage{
		AutoUseResetCard: json.RawMessage(`true`),
	}); err != nil {
		t.Fatal(err)
	}
	if enabled, err := store.Bool(ctx, "reader", AutoUseResetCard); err != nil || !enabled {
		t.Fatalf("saved preference = %v, %v", enabled, err)
	}
	if _, err := store.Merge(ctx, "reader", map[string]json.RawMessage{
		AutoUseResetCard: json.RawMessage(`"true"`),
	}); err != nil {
		t.Fatal(err)
	}
	if enabled, err := store.Bool(ctx, "reader", AutoUseResetCard); err != nil || enabled {
		t.Fatalf("string preference = %v, %v", enabled, err)
	}
}

// Another device's merge holds the account and has written its theme but not
// yet committed. A merge of a different key must wait for it and build on the
// committed document, not on the one it read before the other commit. Only
// Postgres's READ COMMITTED can lose the update, so this runs against it.
func TestAMergeWaitsForTheAccountBeforeItReads(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, dbtest.Postgres(t,
		"a preference merge is a read-then-write, which only the account row lock keeps whole on Postgres"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO users (id, username, username_lower, password_hash, role, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"reader", "reader", "reader", "x", RoleUser, StatusActive, 1, 1); err != nil {
		t.Fatal(err)
	}
	store := NewPreferenceStore(db)

	written := make(chan struct{})
	release := make(chan struct{})
	holder := make(chan error, 1)
	var holderPID int
	go func() {
		holder <- db.Tx(ctx, func(tx *database.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, "reader"); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO user_preferences (user_id, data, updated_at) VALUES (?, ?, ?)`,
				"reader", `{"theme":"dark"}`, 1); err != nil {
				return err
			}
			// The merge has to be seen waiting on this backend, so its pid is
			// read while the transaction is still open.
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
				return err
			}
			close(written)
			<-release
			return nil
		})
	}()
	// The holder can fail before it reaches written. Waiting on written alone
	// would then hang until go test's timeout rather than report the error.
	select {
	case <-written:
	case err := <-holder:
		t.Fatalf("other device's merge: %v", err)
	}

	// A failure below must still let the holder commit: an open transaction
	// keeps the account locked, and the schema cleanup would wait on it forever.
	released := false
	releaseHolder := func() {
		if !released {
			released = true
			close(release)
		}
	}
	defer releaseHolder()

	merged := make(chan error, 1)
	go func() {
		_, err := store.Merge(ctx, "reader", map[string]json.RawMessage{
			"accent": json.RawMessage(`"blue"`),
		})
		merged <- err
	}()
	waitForLockBehind(ctx, t, db, holderPID)
	releaseHolder()
	if err := <-holder; err != nil {
		t.Fatalf("other device's merge: %v", err)
	}
	if err := <-merged; err != nil {
		t.Fatalf("merge: %v", err)
	}

	raw, err := store.Get(ctx, "reader")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["theme"]; !ok {
		t.Fatalf("the other device's theme was dropped: %s", raw)
	}
	if _, ok := got["accent"]; !ok {
		t.Fatalf("this merge's accent was dropped: %s", raw)
	}
}

// waitForLockBehind returns once some backend is waiting on a lock the holder
// owns. A fixed sleep only guesses at how long the merge takes to reach the
// account row: on a slow machine an unlocked merge could still be reading when
// the holder commits, and the test would pass without the two ever overlapping.
//
// Matching on the holder's pid rather than on any lock wait keeps the poll from
// being satisfied by another test waiting on a lock in the same database.
func waitForLockBehind(ctx context.Context, t *testing.T, db *database.DB, holderPID int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock' AND ? = ANY(pg_blocking_pids(pid))`,
			holderPID).Scan(&waiting); err != nil {
			t.Fatalf("watch for the merge to block: %v", err)
		}
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the merge never waited behind the other device's account lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
