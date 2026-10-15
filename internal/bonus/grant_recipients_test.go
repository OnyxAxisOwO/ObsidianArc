package bonus

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
)

// An administrator's grant is spent only from a bar that is switched on. A
// grant into a switched-off bar would be stored, reported and announced, and
// could never be used, so it is refused as GrantTo refuses it.
func TestAGrantIntoASwitchedOffBarIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.account(t, "alice")
	bar := f.bar(t, Bar{Name: "gift"})
	bar.Active = false
	if _, err := f.store.UpdateBar(ctx, nil, bar); err != nil {
		t.Fatal(err)
	}

	_, err := f.store.Grant(ctx, bar.ID, Target{UserIDs: []string{a.ID}}, 2, 0, "admin", "", "test")
	if !errors.Is(err, ErrBarOff) {
		t.Fatalf("grant into a switched-off bar: %v, want ErrBarOff", err)
	}
	var n int
	_ = f.db.QueryRow(ctx, `SELECT COUNT(*) FROM bonus_grants WHERE bar_id = ?`, bar.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("a refused grant left %d rows", n)
	}
}

// The notices after a grant go to exactly the accounts it wrote to, each
// once, however the accounts were named.
func TestAGrantReportsExactlyWhoReceivedIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, b := f.account(t, "alice"), f.account(t, "bob")
	bar := f.bar(t, Bar{Name: "gift"})

	res, err := f.store.Grant(ctx, bar.ID,
		Target{UserIDs: []string{a.ID, a.ID}, Usernames: []string{"Alice", "bob"}},
		1, 0, "admin", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 2 || len(res.UserIDs) != 2 {
		t.Fatalf("granted %d, reported %v; want two accounts, each once", res.Count, res.UserIDs)
	}
	seen := map[string]bool{}
	for _, id := range res.UserIDs {
		seen[id] = true
	}
	if !seen[a.ID] || !seen[b.ID] {
		t.Fatalf("reported %v, want alice and bob", res.UserIDs)
	}
}

// A grant that starts while a switch-off is still uncommitted must wait for it
// and then be refused, rather than read the bar as on and write into it. The
// wait is the row lock the grant takes before reading the switch. SQLite
// serialises write transactions, so the window exists only on PostgreSQL.
func TestAGrantWaitsForASwitchOffInFlight(t *testing.T) {
	if os.Getenv(dbtest.DSNVariable) == "" {
		t.Skipf("set %s: the window exists only under PostgreSQL's READ COMMITTED", dbtest.DSNVariable)
	}
	ctx := context.Background()
	paths := []struct {
		name  string
		grant func(f *fixture, barID, userID string) error
	}{
		{"Grant", func(f *fixture, barID, userID string) error {
			_, err := f.store.Grant(ctx, barID, Target{UserIDs: []string{userID}}, 2, 0, "admin", "", "test")
			return err
		}},
		{"GrantTo", func(f *fixture, barID, userID string) error {
			return f.db.Tx(ctx, func(tx *database.Tx) error {
				_, err := f.store.GrantTo(ctx, tx, barID, userID, 2, 0, "check-in", "")
				return err
			})
		}},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			f := newFixture(t)
			a := f.account(t, "alice")
			bar := f.bar(t, Bar{Name: "gift", ToggleMode: ModeOn})

			done := make(chan error, 1)
			err := f.db.Tx(ctx, func(tx *database.Tx) error {
				// The switch-off holds the bar's row until this transaction commits.
				if _, err := tx.Exec(ctx, `UPDATE bonus_bars SET active = ? WHERE id = ?`, false, bar.ID); err != nil {
					return err
				}
				go func() { done <- p.grant(f, bar.ID, a.ID) }()
				// Commit only once the grant is blocked on the row, so it cannot
				// have read the switch before the commit.
				return waitForRowWait(ctx, f.db, "bonus_bars")
			})
			if err != nil {
				t.Fatalf("holding the switch-off while the grant starts: %v", err)
			}
			if got := <-done; !errors.Is(got, ErrBarOff) {
				t.Fatalf("grant raced a switch-off: %v, want ErrBarOff", got)
			}
			var n int
			if err := f.db.QueryRow(ctx, `SELECT COUNT(*) FROM bonus_grants WHERE bar_id = ?`, bar.ID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("a grant that lost the race left %d rows", n)
			}
		})
	}
}
