package quota

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
)

// A reset card's retry walks the windows again inside the reservation that spent
// the card. The card deletes the current bucket of each window it names before the
// retry, and the refused walk had only locked the windows up to the one that
// refused it. A card that names the month but not the week therefore took the
// month's row before the week's, which the rest of the lock order forbids: a
// settlement in the next five-hour bucket takes the week and then the month, so
// each transaction could hold the row the other was waiting for.
func TestResetCardRetryTakesWindowRowsInOneOrder(t *testing.T) {
	t.Run("five-hour and month card", func(t *testing.T) {
		resetRetryAgainstSettlement(t, []string{"5h", "1m"})
	})
	// The control. A card that names the week takes its rows in order, so the same
	// settlement waits for it and nothing is aborted.
	t.Run("five-hour and week card", func(t *testing.T) {
		resetRetryAgainstSettlement(t, []string{"5h", "1w"})
	})
}

// resetRetryAgainstSettlement drives the two transactions into the order that
// deadlocks, one step at a time, rather than waiting for the scheduler to find it.
// The reservation is refused by the five-hour window and spends a card that names
// cardWindows. The settlement takes the week's row and then waits for the month's.
func resetRetryAgainstSettlement(t *testing.T, cardWindows []string) {
	t.Helper()
	service, db := newServiceOn(t, dbtest.Postgres(t,
		"a reset card's retry must take the window rows in the order every other path does"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Fixed instants, so the test does not depend on the day it runs. An account
	// with no creation time is anchored to the epoch, which makes the five-hour
	// bucket of next the one after the bucket of now, and these two instants share
	// a week and a month.
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	next := now.Add(5 * time.Hour)

	person := account("reset-card-order-user", "")
	key := scopeKey(person.ID)
	if _, err := service.Policies().Save(ctx, Policy{
		Scope:   ScopeGlobal,
		Windows: map[Window]Limits{Window5H: limits(true, ptrInt(1), nil, nil)},
	}); err != nil {
		t.Fatal(err)
	}
	// One request is already in this five-hour bucket, so the next is refused by it.
	if _, err := service.reserve(ctx, person, "", Estimate{}, nil, now); err != nil {
		t.Fatal(err)
	}

	held := make(chan struct{})    // closed once the settlement holds its rows
	proceed := make(chan struct{}) // closed once the reservation is waiting on a row
	atCard := make(chan int, 1)    // the reservation's backend, sent from inside the card

	settled := make(chan error, 1)
	go func() {
		settled <- db.Tx(ctx, func(tx *database.Tx) error {
			// The settlement's order: the minute, the five-hour bucket after, the
			// week, then the month.
			if _, err := bump(ctx, tx, key, WindowTPM, bucketStart(WindowTPM, next, 0), 0, 0, 0); err != nil {
				return err
			}
			if _, err := bump(ctx, tx, key, Window5H, bucketStart(Window5H, next, 0), 0, 0, 0); err != nil {
				return err
			}
			if _, err := bump(ctx, tx, key, WindowWeek, bucketStart(WindowWeek, next, 0), 0, 0, 0); err != nil {
				return err
			}
			close(held)
			select {
			case <-proceed:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := bump(ctx, tx, key, WindowMonth, bucketStart(WindowMonth, next, 0), 0, 0, 0)
			return err
		})
	}()

	reserved := make(chan error, 1)
	go func() {
		_, err := service.reserve(ctx, person, "", Estimate{}, func(ctx context.Context, q database.Queryer, _ Window) ([]string, bool, error) {
			var backend int
			if err := q.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backend); err != nil {
				return nil, false, err
			}
			atCard <- backend
			// Spent only once the settlement holds its rows, so the reservation
			// cannot finish its retry before the settlement is in place.
			select {
			case <-held:
			case <-ctx.Done():
				return nil, false, ctx.Err()
			}
			return cardWindows, true, nil
		}, now)
		reserved <- err
	}()

	var backend int
	select {
	case backend = <-atCard:
	case err := <-reserved:
		t.Fatalf("reservation finished before a card was spent: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := waitUntilBlocked(ctx, db, backend); err != nil {
		t.Fatalf("reservation never waited on the settlement's row: %v", err)
	}
	close(proceed)

	if err := <-reserved; err != nil {
		t.Errorf("reservation with a card for %v: %v", cardWindows, err)
	}
	if err := <-settled; err != nil {
		t.Errorf("settlement in the next five-hour bucket: %v", err)
	}
}

// waitUntilBlocked returns once the backend is waiting for a lock. It polls rather
// than sleeps: the settlement may ask for the month's row only once the reservation
// holds that row and is waiting on the week's, and a fixed pause is only usually
// long enough.
func waitUntilBlocked(ctx context.Context, db *database.DB, backend int) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err := db.QueryRow(ctx,
			`SELECT COALESCE(wait_event_type = 'Lock', false) FROM pg_stat_activity WHERE pid = ?`,
			backend).Scan(&waiting); err != nil {
			return err
		}
		if waiting {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("backend %d is not waiting for a lock", backend)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}
