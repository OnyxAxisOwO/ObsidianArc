package quota

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
)

// Reservations, settlements and releases of one account must take the window
// rows in the same order. Two transactions that take the same rows in opposite
// orders can each hold one and wait for the other, and PostgreSQL then aborts
// one of them with a deadlock (SQLSTATE 40P01). The weekly window is enforced
// and the five-hour one is not, which is the shape that once walked the rows in
// opposite orders: enforced windows were charged first and unenforced ones
// after. Only real goroutines produce the overlap that exposes it.
func TestConcurrentReserveSettleAndReleaseTakeRowsInOneOrder(t *testing.T) {
	service, _ := newServiceOn(t, dbtest.Either(t, filepath.Join(t.TempDir(), "quota.db")))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if _, err := service.Policies().Save(ctx, Policy{
		Scope:   ScopeGlobal,
		Windows: map[Window]Limits{WindowWeek: limits(true, nil, ptrInt(1<<40), nil)},
	}); err != nil {
		t.Fatal(err)
	}
	person := account("lock-order-user", "")
	reserved := Estimate{Tokens: 100, Credits: 0.5}
	actual := Estimate{Tokens: 60, Credits: 0.25}

	const workers, rounds = 8, 25
	var (
		wait    sync.WaitGroup
		mu      sync.Mutex
		settled int64
	)
	for worker := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for round := range rounds {
				res, err := service.Reserve(ctx, person, reserved)
				if err != nil {
					t.Errorf("worker %d reserve: %v", worker, err)
					return
				}
				// Every fourth turn is given back instead of settled, so the
				// release path, which also walks every window, runs against the
				// same rows as the settle path.
				if round%4 == 3 {
					if err := service.Release(ctx, person.ID, res); err != nil {
						t.Errorf("worker %d release: %v", worker, err)
						return
					}
					continue
				}
				if err := service.Settle(ctx, person, reserved, actual); err != nil {
					t.Errorf("worker %d settle: %v", worker, err)
					return
				}
				mu.Lock()
				settled++
				mu.Unlock()
			}
		}()
	}
	wait.Wait()

	summary, err := service.SummaryFor(ctx, person)
	if err != nil {
		t.Fatal(err)
	}
	// Every reservation is one request in every window, whatever became of it.
	// A settled turn leaves what it actually used behind, and a released one
	// leaves nothing, so the totals are exact when no update was lost.
	for _, window := range summary.Windows {
		if window.UsedRequests != workers*rounds {
			t.Errorf("%s counted %d requests, want %d", window.Kind, window.UsedRequests, workers*rounds)
		}
		if want := settled * actual.Tokens; window.UsedTokens != want {
			t.Errorf("%s counted %d tokens, want %d", window.Kind, window.UsedTokens, want)
		}
		if want := float64(settled) * actual.Credits; window.UsedCredits != want {
			t.Errorf("%s counted %v credits, want %v", window.Kind, window.UsedCredits, want)
		}
	}
}
