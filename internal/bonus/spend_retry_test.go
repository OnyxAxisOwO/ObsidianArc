package bonus

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// rivalQueryer lets other takes land on one grant between this take's read of
// it and its claim, which is the window a concurrent request needs. Each rival
// is one committed take on that grant, so the claim it precedes must lose.
// Opening the window on purpose keeps the test independent of scheduling.
type rivalQueryer struct {
	database.Queryer
	t       *testing.T
	grantID string
	rivals  []float64
}

func (r *rivalQueryer) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if len(r.rivals) > 0 && strings.HasPrefix(query, "UPDATE bonus_grants SET used = used + ?") && args[1] == r.grantID {
		amount := r.rivals[0]
		r.rivals = r.rivals[1:]
		if _, err := r.Queryer.Exec(ctx, `UPDATE bonus_grants SET used = used + ? WHERE id = ?`, amount, r.grantID); err != nil {
			r.t.Fatal(err)
		}
	}
	return r.Queryer.Exec(ctx, query, args...)
}

// Three lost claims on the earlier grant used to end its retries, and the
// fallback then gave up on a request the two grants together could cover: 0.4
// was left on the first grant and 0.6 on the second, for a request of 1.
func TestALostClaimIsRetriedWhileTheGrantStillHoldsCredit(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	first := f.bar(t, Bar{Name: "first", ToggleMode: ModeOff})
	second := f.bar(t, Bar{Name: "second", ToggleMode: ModeOff})
	f.give(t, first.ID, a.ID, 1, time.Now().Add(24*time.Hour).UnixMilli())
	f.give(t, second.ID, a.ID, 0.6, time.Now().Add(72*time.Hour).UnixMilli())

	ctx := context.Background()
	var firstGrant string
	if err := f.db.QueryRow(ctx, `SELECT id FROM bonus_grants WHERE bar_id = ? AND user_id = ?`, first.ID, a.ID).Scan(&firstGrant); err != nil {
		t.Fatal(err)
	}

	race := &rivalQueryer{t: t, grantID: firstGrant, rivals: []float64{0.3, 0.2, 0.1}}
	var holds []Hold
	err := f.db.Tx(ctx, func(tx *database.Tx) error {
		race.Queryer = tx
		var err error
		holds, err = f.store.Take(ctx, race, a.ID, "m", 1, Fallback)
		return err
	})
	if err != nil || !near(Total(holds), 1) {
		t.Fatalf("fallback for 1 with 1 of credit left across grants: %v %v", holds, err)
	}
}

// The injected version above proves the retry; this one lets the claims race
// for real. Ten half-credit requests against two grants that together hold
// exactly five credits: each must be covered, the holds must add up to the
// requests, and neither grant may be taken past what it holds.
func TestConcurrentFallbackTakesAreCoveredByWhatTheGrantsHold(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.account(t, "alice")
	first := f.bar(t, Bar{Name: "first", ToggleMode: ModeOff})
	second := f.bar(t, Bar{Name: "second", ToggleMode: ModeOff})
	f.give(t, first.ID, a.ID, 3, time.Now().Add(24*time.Hour).UnixMilli())
	f.give(t, second.ID, a.ID, 2, time.Now().Add(72*time.Hour).UnixMilli())

	const requests = 10
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		taken float64
		errs  []error
	)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var holds []Hold
			err := f.db.Tx(ctx, func(tx *database.Tx) error {
				var err error
				holds, err = f.store.Take(ctx, tx, a.ID, "m", 0.5, Fallback)
				return err
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			taken += Total(holds)
		}()
	}
	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("%d of %d fallback takes were refused, the first with %v; the grants hold what they were asked for", len(errs), requests, errs[0])
	}
	if !near(taken, 5) {
		t.Fatalf("the takes covered %v, want the 5 that were asked for", taken)
	}
	usedFirst, usedSecond := f.used(t, first.ID, a.ID), f.used(t, second.ID, a.ID)
	if usedFirst > 3+1e-6 || usedSecond > 2+1e-6 {
		t.Fatalf("grants taken past what they hold: first %v of 3, second %v of 2", usedFirst, usedSecond)
	}
	if !near(usedFirst+usedSecond, taken) {
		t.Fatalf("the grants record %v but the takes reported %v", usedFirst+usedSecond, taken)
	}
}
