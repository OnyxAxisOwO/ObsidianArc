package quota

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
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

// Every path that writes one account's quota rows must take them in one order,
// or two of them can meet in opposite orders and PostgreSQL aborts one side with
// a deadlock (SQLSTATE 40P01). This runs every such path at once with real
// goroutines: a reservation the five-hour window refuses, paid for by a bar kept
// back or by a reset card; the releases that give those back; settlements; and
// the administrator's resets.
func TestConcurrentResetsSettlesAndFallbacksTakeRowsInOneOrder(t *testing.T) {
	// No token rate: a card's reservation holds the five-hour row while a
	// settlement holds the minute row and waits for that five-hour row. The
	// resets must not reverse that order.
	t.Run("reset cards against settlements", func(t *testing.T) {
		concurrentAccountRun(t, false, false)
	})
	// A token rate makes every release of a bar-paid request take the bar row
	// and then the minute row, so a fallback reservation has to take them in the
	// same order.
	t.Run("fallback bars against releases", func(t *testing.T) {
		concurrentAccountRun(t, true, true)
	})
}

func concurrentAccountRun(t *testing.T, tokenRate, withBar bool) {
	f := newBonusFixture(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// One request per five hours, so most requests are refused by the window and
	// are paid for by a bar or a card. The minute rate is set high enough never to
	// refuse, because a rate row is charged on every path that admits a request.
	policy := Policy{
		Scope:   ScopeGlobal,
		RPM:     ptrInt(1 << 30),
		Windows: map[Window]Limits{Window5H: limits(true, ptrInt(1), nil, nil)},
	}
	if tokenRate {
		policy.TPM = ptrInt(1 << 30)
	}
	if _, err := f.svc.Policies().Save(ctx, policy); err != nil {
		t.Fatal(err)
	}
	person := f.person(t, "lock-order-user")
	var kept bonus.Bar
	if withBar {
		// Two credits: a fallback can take its bar row while another fallback's
		// refund of that row has not committed, which is the overlap that
		// deadlocks. A grant that is fully in use reads as spent, so the holds
		// have to leave room for that overlap to reach the row at all. A third
		// concurrent request finds the bar empty and goes to a card.
		kept = f.bar(t, "kept back", bonus.KindBonus, bonus.ModeOff, 2, person)
	}

	var (
		wait             sync.WaitGroup
		fallbacks, cards atomic.Int64
	)
	fullCard := func(context.Context, database.Queryer, Window) ([]string, bool, error) {
		cards.Add(1)
		return nil, true, nil
	}

	const workers, rounds = 4, 60
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range rounds {
				res, err := f.svc.ReserveFor(ctx, person, "m", Estimate{Tokens: 100, Credits: 1}, fullCard)
				if err != nil {
					if _, refused := AsExceeded(err); !refused {
						t.Errorf("reserve: %v", err)
						return
					}
					continue
				}
				if res.funding != nil {
					fallbacks.Add(1)
				}
				if err := f.svc.Release(ctx, person.ID, res); err != nil {
					t.Errorf("release: %v", err)
					return
				}
			}
		}()
	}
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range rounds {
				if err := f.svc.Settle(ctx, person, Estimate{}, Estimate{Tokens: 60, Credits: 0.25}); err != nil {
					t.Errorf("settle: %v", err)
					return
				}
			}
		}()
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		for range 30 {
			if err := f.svc.ResetWindows(ctx, []string{person.ID}, []string{"5h"}); err != nil {
				t.Errorf("reset windows: %v", err)
				return
			}
			if err := f.svc.ResetAll(ctx); err != nil {
				t.Errorf("reset all: %v", err)
				return
			}
		}
	}()
	wait.Wait()

	// Each shape must reach the path it exists for. Without a bar that is the
	// card. With one it is the fallback: on a single write connection the holds
	// never overlap enough to empty the bar, so a card is not required there.
	if withBar && fallbacks.Load() == 0 {
		t.Fatal("no reservation was paid from the bar, so the fallback path was not run")
	}
	if !withBar && cards.Load() == 0 {
		t.Fatal("no reset card was spent, so the card path was not run")
	}
	if withBar {
		// Every fallback hold was released, so the bar is back where it started.
		if used := f.barUsed(t, kept); !within(used, 0) {
			t.Errorf("the bar carries %v after every fallback was released", used)
		}
	}

	var negative int
	if err := f.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM usage_counters WHERE requests < 0 OR tokens < 0 OR credits < 0`).Scan(&negative); err != nil {
		t.Fatal(err)
	}
	if negative != 0 {
		t.Errorf("%d counters went below zero", negative)
	}

	// The run leaves counters whose exact values depend on how the resets and
	// the reservations interleaved, so they are cleared, and a short sequence
	// that runs alone is what is counted exactly.
	if err := f.svc.ResetAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Policies().Save(ctx, Policy{
		Scope: ScopeGlobal,
		RPM:   ptrInt(1 << 30),
		TPM:   ptrInt(1 << 30),
	}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := f.svc.Reserve(ctx, person, Estimate{Tokens: 100}); err != nil {
			t.Fatal(err)
		}
		if err := f.svc.Settle(ctx, person, Estimate{Tokens: 100}, Estimate{Tokens: 70, Credits: 0.5}); err != nil {
			t.Fatal(err)
		}
	}
	// Three requests, each settled from a hundred tokens down to seventy: one
	// request in every counter, and the rate counters hold it once.
	want := map[Window]counter{
		WindowRPM:   {Requests: 3},
		WindowTPM:   {Tokens: 210, Credits: 1.5},
		Window5H:    {Requests: 3, Tokens: 210, Credits: 1.5},
		WindowWeek:  {Requests: 3, Tokens: 210, Credits: 1.5},
		WindowMonth: {Requests: 3, Tokens: 210, Credits: 1.5},
	}
	for window, expect := range want {
		var got counter
		if err := f.db.QueryRow(ctx,
			`SELECT COALESCE(SUM(requests), 0), COALESCE(SUM(tokens), 0), COALESCE(SUM(credits), 0)
			 FROM usage_counters WHERE scope_key = ? AND window_kind = ?`,
			scopeKey(person.ID), string(window)).Scan(&got.Requests, &got.Tokens, &got.Credits); err != nil {
			t.Fatal(err)
		}
		if got.Requests != expect.Requests || got.Tokens != expect.Tokens || !within(got.Credits, expect.Credits) {
			t.Errorf("%s holds %d requests, %d tokens and %v credits, want %d, %d and %v",
				window, got.Requests, got.Tokens, got.Credits, expect.Requests, expect.Tokens, expect.Credits)
		}
	}
}
