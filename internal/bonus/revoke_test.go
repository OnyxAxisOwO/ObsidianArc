package bonus

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
)

// waitForRowWait returns once some session is blocked on a statement that
// touches table, and fails after a few seconds. It runs while the spend holds
// the row, so it reads the catalogue on another connection and never waits on
// that lock itself.
func waitForRowWait(ctx context.Context, db *database.DB, table string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		if err := db.QueryRow(ctx,
			`SELECT COUNT(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE ?`,
			"%"+table+"%").Scan(&waiting); err != nil {
			return err
		}
		if waiting > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no session ever waited on %s", table)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Revoke reports what it removed. On PostgreSQL's READ COMMITTED a read taken
// before the grant's row lock can miss a spend that commits in between, so the
// figure reported would differ from what the grant lost. SQLite serialises
// write transactions and has no such window, so this runs only on PostgreSQL.
func TestRevokeReportsWhatItRemovedWhenASpendCommitsMidRevoke(t *testing.T) {
	if os.Getenv(dbtest.DSNVariable) == "" {
		t.Skipf("set %s: the window only exists under PostgreSQL's READ COMMITTED", dbtest.DSNVariable)
	}
	ctx := context.Background()
	f := newFixture(t)
	a := f.account(t, "alice")
	bar := f.bar(t, Bar{Name: "gift", ToggleMode: ModeOn})
	f.give(t, bar.ID, a.ID, 10, 0)
	rows, _, err := f.store.GrantsInBar(ctx, bar.ID, 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("grants: %d (%v), want the one grant", len(rows), err)
	}
	grantID := rows[0].ID

	type outcome struct {
		revoked float64
		err     error
	}
	done := make(chan outcome, 1)
	err = f.db.Tx(ctx, func(tx *database.Tx) error {
		// A spend of three credits that has taken the row but not committed.
		if _, err := tx.Exec(ctx, `UPDATE bonus_grants SET used = used + 3 WHERE id = ?`, grantID); err != nil {
			return err
		}
		go func() {
			revoked, err := f.store.Revoke(ctx, grantID)
			done <- outcome{revoked, err}
		}()
		// Commit only once Revoke is blocked on the row. Before that, a Revoke
		// that reads first would already have seen the grant without the spend,
		// and the test would prove nothing.
		return waitForRowWait(ctx, f.db, "bonus_grants")
	})
	if err != nil {
		t.Fatalf("holding the grant while revoking: %v", err)
	}
	got := <-done
	if got.err != nil {
		t.Fatalf("revoke: %v", got.err)
	}

	var amount, used float64
	if err := f.db.QueryRow(ctx, `SELECT amount, used FROM bonus_grants WHERE id = ?`, grantID).Scan(&amount, &used); err != nil {
		t.Fatal(err)
	}
	// Ten granted, three spent before the revoke closed it: seven were removed.
	if !near(amount, 3) || !near(used, 3) {
		t.Fatalf("closed grant has amount %v and used %v, want both 3", amount, used)
	}
	if !near(got.revoked, 7) {
		t.Fatalf("revoke reported %v removed, but the grant gave up 7", got.revoked)
	}
}

// Revoke cuts a grant down to what it has spent, and a hold still in flight is
// part of what it has spent. The turn that hold pays for then finishes: its
// hold is given back, or its cost is settled and the hold given back after. No
// way of finishing may turn the revoked grant back into credit an account can
// spend, because the revoke is what took that credit away.
func TestAHoldFinishingAfterARevokeReopensNothing(t *testing.T) {
	finishes := []struct {
		name   string
		finish func(f *fixture, holds []Hold) error
	}{
		{"refund", func(f *fixture, holds []Hold) error {
			return f.store.Refund(context.Background(), f.db, holds)
		}},
		{"settle, then refund", func(f *fixture, holds []Hold) error {
			if _, err := f.store.Settle(context.Background(), f.db, holds, 1, false); err != nil {
				return err
			}
			return f.store.Refund(context.Background(), f.db, holds)
		}},
		{"refund, then settle", func(f *fixture, holds []Hold) error {
			if err := f.store.Refund(context.Background(), f.db, holds); err != nil {
				return err
			}
			_, err := f.store.Settle(context.Background(), f.db, holds, 1, true)
			return err
		}},
	}
	for _, c := range finishes {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			a := f.account(t, "alice")
			bar := f.bar(t, Bar{Name: "gift", ToggleMode: ModeOn})
			f.give(t, bar.ID, a.ID, 10, 0)
			holds, err := f.take(t, a.ID, "m", 2, Priority)
			if err != nil || !near(Total(holds), 2) {
				t.Fatalf("the turn's hold: %v, %v", holds, err)
			}
			rows, _, err := f.store.GrantsInBar(ctx, bar.ID, 10, 0)
			if err != nil || len(rows) != 1 {
				t.Fatalf("grants: %d (%v), want the one grant", len(rows), err)
			}
			revoked, err := f.store.Revoke(ctx, rows[0].ID)
			if err != nil || !near(revoked, 8) {
				t.Fatalf("revoke removed %v (%v), want the 8 that were not held", revoked, err)
			}

			if err := c.finish(f, holds); err != nil {
				t.Fatalf("finishing the turn: %v", err)
			}
			spent, err := f.take(t, a.ID, "m", 5, Priority)
			if err != nil {
				t.Fatal(err)
			}
			if got := Total(spent); !near(got, 0) {
				t.Fatalf("%v credits of the revoked grant became spendable again", got)
			}
		})
	}
}
