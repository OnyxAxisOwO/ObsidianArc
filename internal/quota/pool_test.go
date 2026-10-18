package quota

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func (f *bonusFixture) pool(t *testing.T, mode bonus.Mode, models []string, pool bonus.Pool) bonus.Bar {
	t.Helper()
	bar, err := f.bonus.CreateBar(context.Background(), bonus.Bar{
		Name: "shared", Kind: bonus.KindShared, ToggleMode: mode, DefaultOn: true, ShowTotal: true,
		ModelIDs: models, Pool: &pool,
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	return bar
}

func (f *bonusFixture) poolUsed(t *testing.T, bar bonus.Bar) float64 {
	t.Helper()
	var used float64
	if err := f.db.QueryRow(context.Background(), `SELECT used FROM bonus_pools WHERE bar_id = ?`, bar.ID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	return used
}

func (f *bonusFixture) drawn(t *testing.T, bar bonus.Bar, u user.User) float64 {
	t.Helper()
	var used float64
	if err := f.db.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(used), 0) FROM bonus_pool_usage WHERE bar_id = ? AND user_id = ?`, bar.ID, u.ID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	return used
}

// A turn on a forced pool is the pool's, and the window never hears of it.
func TestAForcedPoolPaysBeforeTheWindow(t *testing.T) {
	f := newBonusFixture(t, 10)
	a := f.person(t, "alice")
	bar := f.pool(t, bonus.ModeOn, []string{"m"}, bonus.Pool{Amount: 100})
	ctx := WithLedger(context.Background())

	res, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Tokens: 1000, Credits: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !within(f.poolUsed(t, bar), 4) || f.windowCredits(t, a) != 0 {
		t.Fatalf("reserved: pool used %v, window %v", f.poolUsed(t, bar), f.windowCredits(t, a))
	}
	if err := f.svc.SettleTurn(ctx, a, Estimate{Tokens: 700, Credits: 3}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Release(ctx, a.ID, res); err != nil {
		t.Fatal(err)
	}
	if !within(f.poolUsed(t, bar), 3) || !within(f.drawn(t, bar, a), 3) || f.windowCredits(t, a) != 0 {
		t.Fatalf("settled: pool %v, drawn %v, window %v", f.poolUsed(t, bar), f.drawn(t, bar, a), f.windowCredits(t, a))
	}
}

// Forced with somewhere to fall: an empty pool leaves the request to the
// account's own allowance, which is what 'on' means and 'only' does not.
func TestAnEmptyForcedPoolFallsBackToTheWindow(t *testing.T) {
	f := newBonusFixture(t, 10)
	a := f.person(t, "alice")
	bar := f.pool(t, bonus.ModeOn, nil, bonus.Pool{Amount: 1})
	if _, err := f.svc.ReserveFor(WithLedger(context.Background()), a, "m", Estimate{Credits: 4}, nil); err != nil {
		t.Fatal(err)
	}
	if !within(f.poolUsed(t, bar), 1) || !within(f.windowCredits(t, a), 3) {
		t.Fatalf("pool %v, window %v; want 1 and 3", f.poolUsed(t, bar), f.windowCredits(t, a))
	}
}

func TestAnOnlyPoolNeverTouchesTheWindowAndRefusesWhenEmpty(t *testing.T) {
	f := newBonusFixture(t, 10)
	a := f.person(t, "alice")
	bar := f.pool(t, bonus.ModeOnly, []string{"m"}, bonus.Pool{Amount: 5})
	// A bar of the account's own is not asked either: the pool is the only payer.
	own := f.bar(t, "gift", bonus.KindBonus, bonus.ModeOn, 100, a)

	ctx := WithLedger(context.Background())
	if _, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 4}, nil); err != nil {
		t.Fatal(err)
	}
	if !within(f.poolUsed(t, bar), 4) || f.windowCredits(t, a) != 0 || f.barUsed(t, own) != 0 {
		t.Fatalf("pool %v, window %v, own bar %v", f.poolUsed(t, bar), f.windowCredits(t, a), f.barUsed(t, own))
	}
	_, err := f.svc.ReserveFor(WithLedger(context.Background()), a, "m", Estimate{Credits: 4}, nil)
	var empty *bonus.PoolExhaustedError
	if !errors.As(err, &empty) || empty.Capped {
		t.Fatalf("a pool with 1 left was asked for 4: %v", err)
	}
	if f.windowCredits(t, a) != 0 || !within(f.poolUsed(t, bar), 4) {
		t.Fatalf("a refusal charged something: pool %v, window %v", f.poolUsed(t, bar), f.windowCredits(t, a))
	}
	var answer *httpx.Error
	if !errors.As(TranslateError(err), &answer) || answer.Status != http.StatusTooManyRequests || answer.Code != "pool_exhausted" {
		t.Fatalf("the refusal reads as %v", TranslateError(err))
	}

	// A model the pool does not name is the window's, as before.
	if _, err := f.svc.ReserveFor(WithLedger(context.Background()), a, "other", Estimate{Credits: 2}, nil); err != nil {
		t.Fatal(err)
	}
	if !within(f.barUsed(t, own), 2) {
		t.Fatalf("another model was not paid as before: own bar %v", f.barUsed(t, own))
	}
}

// What an only-pool turn costs beyond its reservation still goes to the pool:
// the windows were never part of it.
func TestAnOnlyPoolCarriesTheOvershoot(t *testing.T) {
	f := newBonusFixture(t, 10)
	a := f.person(t, "alice")
	bar := f.pool(t, bonus.ModeOnly, nil, bonus.Pool{Amount: 5})
	ctx := WithLedger(context.Background())
	res, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SettleTurn(ctx, a, Estimate{Credits: 7}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Release(ctx, a.ID, res); err != nil {
		t.Fatal(err)
	}
	if !within(f.poolUsed(t, bar), 7) || f.windowCredits(t, a) != 0 {
		t.Fatalf("pool %v, window %v; want 7 and 0", f.poolUsed(t, bar), f.windowCredits(t, a))
	}
}

func TestTheDailyLimitHoldsEachAccountToItsShare(t *testing.T) {
	f := newBonusFixture(t, 10)
	a, b := f.person(t, "alice"), f.person(t, "bob")
	bar := f.pool(t, bonus.ModeOnly, nil, bonus.Pool{Amount: 100, PerUserDaily: 5})
	for i := 0; i < 2; i++ {
		if _, err := f.svc.ReserveFor(WithLedger(context.Background()), a, "m", Estimate{Credits: 2}, nil); err != nil {
			t.Fatal(err)
		}
	}
	_, err := f.svc.ReserveFor(WithLedger(context.Background()), a, "m", Estimate{Credits: 2}, nil)
	var capped *bonus.PoolExhaustedError
	if !errors.As(err, &capped) || !capped.Capped || capped.ResetsAt <= time.Now().UnixMilli() {
		t.Fatalf("alice past her share: %v", err)
	}
	// Bob's share is his own.
	if _, err := f.svc.ReserveFor(WithLedger(context.Background()), b, "m", Estimate{Credits: 5}, nil); err != nil {
		t.Fatal(err)
	}
	if !within(f.drawn(t, bar, a), 4) || !within(f.drawn(t, bar, b), 5) {
		t.Fatalf("drawn: alice %v, bob %v", f.drawn(t, bar, a), f.drawn(t, bar, b))
	}
}

// An account that switched an optional pool off keeps it for when its own
// allowance is spent, as with any bar of its own switched off.
func TestAnOptionalPoolSwitchedOffIsKeptBack(t *testing.T) {
	f := newBonusFixture(t, 10)
	a := f.person(t, "alice")
	bar := f.pool(t, bonus.ModeUser, nil, bonus.Pool{Amount: 100})
	if err := f.bonus.SetChoice(context.Background(), a.ID, bar.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ReserveFor(WithLedger(context.Background()), a, "m", Estimate{Credits: 8}, nil); err != nil {
		t.Fatal(err)
	}
	if f.poolUsed(t, bar) != 0 || !within(f.windowCredits(t, a), 8) {
		t.Fatalf("switched off: pool %v, window %v", f.poolUsed(t, bar), f.windowCredits(t, a))
	}
	if _, err := f.svc.ReserveFor(WithLedger(context.Background()), a, "m", Estimate{Credits: 8}, nil); err != nil {
		t.Fatal(err)
	}
	if !within(f.poolUsed(t, bar), 8) {
		t.Fatalf("the window was spent and the pool did not step in: pool %v", f.poolUsed(t, bar))
	}
}

// Many accounts at once on one balance, with real goroutines: the pool and
// each account's share are both held exactly.
func TestConcurrentDrawsNeverOverdrawAPoolOrAShare(t *testing.T) {
	f := newBonusFixture(t, 10)
	people := []user.User{f.person(t, "ann"), f.person(t, "ben"), f.person(t, "cat"), f.person(t, "dan")}
	bar := f.pool(t, bonus.ModeOnly, nil, bonus.Pool{Amount: 30, PerUserDaily: 10})
	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok int
	)
	for i := 0; i < 60; i++ {
		u := people[i%len(people)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.svc.ReserveFor(WithLedger(context.Background()), u, "m", Estimate{Credits: 1}, nil)
			if err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			} else if !errors.Is(err, bonus.ErrInsufficient) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 30 || !within(f.poolUsed(t, bar), 30) {
		t.Fatalf("%d requests paid, pool used %v; the pool holds exactly 30", ok, f.poolUsed(t, bar))
	}
	for _, u := range people {
		if d := f.drawn(t, bar, u); d > 10+1e-6 {
			t.Fatalf("%s drew %v, past a share of 10", u.Username, d)
		}
	}
}

func TestAnAccountWithNoLimitsDrawsNothingFromAPool(t *testing.T) {
	f := newBonusFixture(t, 10)
	admin, err := f.users.Create(context.Background(), nil, user.CreateInput{
		Username: "root", PasswordHash: "x", Role: user.RoleAdmin, Status: user.StatusActive, GroupID: f.groupID,
	})
	if err != nil {
		t.Fatal(err)
	}
	bar := f.pool(t, bonus.ModeOnly, nil, bonus.Pool{Amount: 1})
	if _, err := f.svc.ReserveFor(WithLedger(context.Background()), admin, "m", Estimate{Credits: 5}, nil); err != nil {
		t.Fatal(err)
	}
	if f.poolUsed(t, bar) != 0 {
		t.Fatalf("an exempt administrator drew %v", f.poolUsed(t, bar))
	}
}
