package quota

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type bonusFixture struct {
	svc     *Service
	bonus   *bonus.Store
	db      *database.DB
	users   *user.Store
	groupID string
}

// A service with a window of ten credits every five hours for everybody, and
// bonus bars beside it.
func newBonusFixture(t *testing.T, windowCredits float64) *bonusFixture {
	t.Helper()
	svc, db := newServiceOn(t, dbtest.Either(t, filepath.Join(t.TempDir(), "quota-bonus.db")))
	b := bonus.NewStore(db)
	svc.SetBonus(b)
	ctx := context.Background()
	g, err := group.NewStore(db).Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Policies().Save(ctx, Policy{Scope: ScopeGlobal, Windows: map[Window]Limits{
		Window5H: limits(true, nil, nil, ptrFloat(windowCredits)),
	}}); err != nil {
		t.Fatal(err)
	}
	return &bonusFixture{svc: svc, bonus: b, db: db, users: user.NewStore(db), groupID: g.ID}
}

func (f *bonusFixture) person(t *testing.T, name string) user.User {
	t.Helper()
	u, err := f.users.Create(context.Background(), nil, user.CreateInput{
		Username: name, PasswordHash: "x", Role: user.RoleUser, Status: user.StatusActive, GroupID: f.groupID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f *bonusFixture) bar(t *testing.T, name string, kind bonus.Kind, mode bonus.Mode, amount float64, u user.User) bonus.Bar {
	t.Helper()
	ctx := context.Background()
	bar, err := f.bonus.CreateBar(ctx, bonus.Bar{Name: name, Kind: kind, ToggleMode: mode, DefaultOn: true, ShowTotal: true}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.bonus.Grant(ctx, bar.ID, bonus.Target{UserIDs: []string{u.ID}}, amount, 0, "admin", "", ""); err != nil {
		t.Fatal(err)
	}
	return bar
}

func (f *bonusFixture) windowCredits(t *testing.T, u user.User) float64 {
	t.Helper()
	var credits float64
	if err := f.db.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(credits), 0) FROM usage_counters WHERE scope_key = ? AND window_kind = '5h'`, scopeKey(u.ID)).Scan(&credits); err != nil {
		t.Fatal(err)
	}
	return credits
}

func (f *bonusFixture) barUsed(t *testing.T, bar bonus.Bar) float64 {
	t.Helper()
	var used float64
	if err := f.db.QueryRow(context.Background(), `SELECT COALESCE(SUM(used), 0) FROM bonus_grants WHERE bar_id = ?`, bar.ID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	return used
}

func within(a, b float64) bool { d := a - b; return d < 1e-6 && d > -1e-6 }

// rates replaces the instance policy with burst limits beside the window, for
// the tests that hold a bar to them.
func (f *bonusFixture) rates(t *testing.T, rpm, tpm *int64, windowCredits float64) {
	t.Helper()
	if _, err := f.svc.Policies().Save(context.Background(), Policy{Scope: ScopeGlobal, RPM: rpm, TPM: tpm, Windows: map[Window]Limits{
		Window5H: limits(true, nil, nil, ptrFloat(windowCredits)),
	}}); err != nil {
		t.Fatal(err)
	}
}

// minute is what the per-minute counters hold for an account.
func (f *bonusFixture) minute(t *testing.T, u user.User, window Window) (requests, tokens int64) {
	t.Helper()
	if err := f.db.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(requests), 0), COALESCE(SUM(tokens), 0) FROM usage_counters WHERE scope_key = ? AND window_kind = ?`,
		scopeKey(u.ID), string(window)).Scan(&requests, &tokens); err != nil {
		t.Fatal(err)
	}
	return requests, tokens
}

// A bar that is switched on pays first, and what it pays does not count
// against the window: that is what makes it worth switching on.
func TestABarSwitchedOnPaysBeforeTheWindowAndTheWindowIsUntouched(t *testing.T) {
	f := newBonusFixture(t, 10)
	a := f.person(t, "alice")
	bar := f.bar(t, "gift", bonus.KindBonus, bonus.ModeOn, 100, a)
	ctx := WithLedger(context.Background())

	res, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Tokens: 1000, Credits: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !within(f.barUsed(t, bar), 4) || f.windowCredits(t, a) != 0 {
		t.Fatalf("reserved: bar used %v, window %v", f.barUsed(t, bar), f.windowCredits(t, a))
	}
	// The turn cost 3 of the 4 reserved: the reservation goes back, the cost is
	// charged, and the window still has seen nothing.
	if err := f.svc.SettleTurn(ctx, a, Estimate{Tokens: 700, Credits: 3}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Release(ctx, a.ID, res); err != nil {
		t.Fatal(err)
	}
	if !within(f.barUsed(t, bar), 3) || f.windowCredits(t, a) != 0 {
		t.Fatalf("settled: bar used %v, window %v", f.barUsed(t, bar), f.windowCredits(t, a))
	}
}

func TestTheOrderOfReleaseAndSettleDoesNotChangeWhatTheBarCarries(t *testing.T) {
	for _, releaseFirst := range []bool{true, false} {
		f := newBonusFixture(t, 10)
		a := f.person(t, "alice")
		bar := f.bar(t, "gift", bonus.KindBonus, bonus.ModeOn, 100, a)
		ctx := WithLedger(context.Background())
		res, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 4}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if releaseFirst {
			_ = f.svc.Release(ctx, a.ID, res)
			_ = f.svc.SettleTurn(ctx, a, Estimate{Credits: 3})
		} else {
			_ = f.svc.SettleTurn(ctx, a, Estimate{Credits: 3})
			_ = f.svc.Release(ctx, a.ID, res)
		}
		if !within(f.barUsed(t, bar), 3) {
			t.Fatalf("release first=%v: the bar carries %v", releaseFirst, f.barUsed(t, bar))
		}
	}
}

func TestARequestThatNeverRanCostsTheBarNothing(t *testing.T) {
	f := newBonusFixture(t, 10)
	a := f.person(t, "alice")
	bar := f.bar(t, "gift", bonus.KindBonus, bonus.ModeOn, 100, a)
	ctx := WithLedger(context.Background())
	res, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Release(ctx, a.ID, res); err != nil {
		t.Fatal(err)
	}
	if f.barUsed(t, bar) != 0 {
		t.Fatalf("the bar carries %v for a request that never ran", f.barUsed(t, bar))
	}
}

// A bar with less in it than the request could cost pays what it has and the
// window carries the rest, in proportion.
func TestABarThatCannotCoverItAllLeavesTheRestToTheWindow(t *testing.T) {
	f := newBonusFixture(t, 10)
	a := f.person(t, "alice")
	bar := f.bar(t, "gift", bonus.KindBonus, bonus.ModeOn, 1, a)
	ctx := WithLedger(context.Background())
	res, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Tokens: 1000, Credits: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !within(f.barUsed(t, bar), 1) || !within(f.windowCredits(t, a), 3) {
		t.Fatalf("bar %v window %v; want 1 and 3", f.barUsed(t, bar), f.windowCredits(t, a))
	}
	_ = f.svc.SettleTurn(ctx, a, Estimate{Tokens: 1000, Credits: 4})
	_ = f.svc.Release(ctx, a.ID, res)
	if !within(f.barUsed(t, bar), 1) || !within(f.windowCredits(t, a), 3) {
		t.Fatalf("settled: bar %v window %v", f.barUsed(t, bar), f.windowCredits(t, a))
	}
}

// When the window has nothing left, the bars that were kept back pay — whole,
// and without touching the window.
func TestAFallbackBarPaysWhenTheWindowIsSpentAndLeavesTheWindowAlone(t *testing.T) {
	f := newBonusFixture(t, 5)
	a := f.person(t, "alice")
	off := f.bar(t, "kept back", bonus.KindBonus, bonus.ModeOff, 100, a)
	ctx := WithLedger(context.Background())

	first, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.barUsed(t, off) != 0 {
		t.Fatal("a bar kept back was spent while the window had room")
	}
	_ = first
	before := f.windowCredits(t, a)
	second, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 2}, nil)
	if err != nil {
		t.Fatalf("with the window spent and a bar kept back: %v", err)
	}
	if !within(f.barUsed(t, off), 2) || f.windowCredits(t, a) != before {
		t.Fatalf("bar used %v, window %v (was %v)", f.barUsed(t, off), f.windowCredits(t, a), before)
	}
	_ = f.svc.Release(ctx, a.ID, second)
	if f.barUsed(t, off) != 0 {
		t.Fatalf("released, the bar still carries %v", f.barUsed(t, off))
	}
}

func TestAFallbackThatCannotCoverTheRequestWholeIsRefusedAsBefore(t *testing.T) {
	f := newBonusFixture(t, 5)
	a := f.person(t, "alice")
	off := f.bar(t, "small", bonus.KindBonus, bonus.ModeOff, 1, a)
	ctx := WithLedger(context.Background())
	if _, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 5}, nil); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 2}, nil)
	if _, ok := AsExceeded(err); !ok {
		t.Fatalf("want the window's refusal, got %v", err)
	}
	if f.barUsed(t, off) != 0 {
		t.Fatalf("a refused request kept %v of the bar", f.barUsed(t, off))
	}
}

// The reserve is the last of the bars; a reset card comes after all of them.
func TestABarKeptBackIsSpentBeforeAResetCardIs(t *testing.T) {
	f := newBonusFixture(t, 5)
	a := f.person(t, "alice")
	f.bar(t, "spare", bonus.KindReserve, bonus.ModeOff, 3, a)
	ctx := WithLedger(context.Background())
	if _, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 5}, nil); err != nil {
		t.Fatal(err)
	}
	spent := 0
	reset := func(context.Context, database.Queryer, Window) ([]string, bool, error) {
		spent++
		return nil, true, nil
	}
	if _, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 2}, reset); err != nil {
		t.Fatal(err)
	}
	if spent != 0 {
		t.Fatalf("a card was spent while a reserve could pay: %d", spent)
	}
	// With nothing kept back that could pay, the card is what is left.
	if _, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 4}, reset); err != nil {
		t.Fatal(err)
	}
	if spent != 1 {
		t.Fatalf("the card was spent %d times", spent)
	}
}

func TestABarOnlyPaysForTheModelsItCovers(t *testing.T) {
	f := newBonusFixture(t, 10)
	a := f.person(t, "alice")
	bar, err := f.bonus.CreateBar(context.Background(), bonus.Bar{Name: "cheap only", ToggleMode: bonus.ModeOn, ModelIDs: []string{"cheap"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.bonus.Grant(context.Background(), bar.ID, bonus.Target{UserIDs: []string{a.ID}}, 50, 0, "admin", "", ""); err != nil {
		t.Fatal(err)
	}
	ctx := WithLedger(context.Background())
	if _, err := f.svc.ReserveFor(ctx, a, "expensive", Estimate{Credits: 2}, nil); err != nil {
		t.Fatal(err)
	}
	if f.barUsed(t, bar) != 0 || !within(f.windowCredits(t, a), 2) {
		t.Fatalf("bar %v window %v", f.barUsed(t, bar), f.windowCredits(t, a))
	}
}

func TestAnAccountWithNoLimitsSpendsNoBar(t *testing.T) {
	f := newBonusFixture(t, 10)
	a := f.person(t, "alice")
	bar := f.bar(t, "gift", bonus.KindBonus, bonus.ModeOn, 100, a)
	if _, err := f.svc.Policies().Save(context.Background(), Policy{Scope: ScopeUser, ScopeID: a.ID,
		Windows: map[Window]Limits{Window5H: {Enabled: ptrBool(false)}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ReserveFor(context.Background(), a, "m", Estimate{Credits: 4}, nil); err != nil {
		t.Fatal(err)
	}
	if f.barUsed(t, bar) != 0 {
		t.Fatalf("an account nothing limits spent %v of a bar", f.barUsed(t, bar))
	}
}

// Many requests at once, a bar and a window that between them cannot cover
// them all: what is charged to each never exceeds what it has.
func TestConcurrentRequestsNeverOverSpendABarOrTheWindow(t *testing.T) {
	f := newBonusFixture(t, 10)
	a := f.person(t, "alice")
	bar := f.bar(t, "gift", bonus.KindBonus, bonus.ModeOn, 10, a)
	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok int
	)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.svc.ReserveFor(WithLedger(context.Background()), a, "m", Estimate{Credits: 1}, nil)
			if err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			} else if _, exceeded := AsExceeded(err); !exceeded && !errors.Is(err, context.Canceled) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if f.barUsed(t, bar) > 10+1e-6 || f.windowCredits(t, a) > 10+1e-6 {
		t.Fatalf("bar %v (of 10), window %v (of 10)", f.barUsed(t, bar), f.windowCredits(t, a))
	}
	if ok != 20 {
		t.Fatalf("%d of 40 requests were paid for; the bar and the window can cover exactly 20", ok)
	}
}

// A bar is more allowance, not a way round the burst limits that protect the
// providers behind the server: a request it pays for whole still counts.
func TestABarDoesNotTakeARequestOutOfTheRequestRate(t *testing.T) {
	f := newBonusFixture(t, 10)
	f.rates(t, ptrInt(2), nil, 10)
	a := f.person(t, "alice")
	bar := f.bar(t, "gift", bonus.KindBonus, bonus.ModeOn, 100, a)
	ctx := WithLedger(context.Background())

	for i := 0; i < 2; i++ {
		if _, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Tokens: 10, Credits: 1}, nil); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	_, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Tokens: 10, Credits: 1}, nil)
	exceeded, ok := AsExceeded(err)
	if !ok || exceeded.Window != WindowRPM {
		t.Fatalf("the third request in a minute was not refused by the rate: %v", err)
	}
	// What was refused took nothing from the bar, and the window never saw any of it.
	if !within(f.barUsed(t, bar), 2) || f.windowCredits(t, a) != 0 {
		t.Fatalf("bar used %v, window %v", f.barUsed(t, bar), f.windowCredits(t, a))
	}
}

func TestABarPaidRequestCountsTowardTheTokenRateAndGivesItBack(t *testing.T) {
	for _, settleFirst := range []bool{false, true} {
		f := newBonusFixture(t, 10)
		f.rates(t, nil, ptrInt(1000), 10)
		a := f.person(t, "alice")
		bar := f.bar(t, "gift", bonus.KindBonus, bonus.ModeOn, 100, a)
		ctx := WithLedger(context.Background())

		res, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Tokens: 600, Credits: 1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, tokens := f.minute(t, a, WindowTPM); tokens != 600 {
			t.Fatalf("settleFirst=%v: the minute holds %d tokens for a request the bar paid", settleFirst, tokens)
		}
		// Room for 400 more, not 1000: the bar paid for the first request and
		// the limit still counted it.
		_, err = f.svc.ReserveFor(WithLedger(context.Background()), a, "m", Estimate{Tokens: 600, Credits: 1}, nil)
		if exceeded, ok := AsExceeded(err); !ok || exceeded.Window != WindowTPM {
			t.Fatalf("settleFirst=%v: the token rate did not refuse it: %v", settleFirst, err)
		}
		if !within(f.barUsed(t, bar), 1) {
			t.Fatalf("settleFirst=%v: a refused request left %v in the bar", settleFirst, f.barUsed(t, bar))
		}

		// The turn used 300 tokens of the 600 held: whichever of the two comes
		// first, the minute ends holding what was used, and the window nothing.
		if settleFirst {
			_ = f.svc.SettleTurn(ctx, a, Estimate{Tokens: 300, Credits: 0.5})
			_ = f.svc.Release(ctx, a.ID, res)
		} else {
			_ = f.svc.Release(ctx, a.ID, res)
			_ = f.svc.SettleTurn(ctx, a, Estimate{Tokens: 300, Credits: 0.5})
		}
		if _, tokens := f.minute(t, a, WindowTPM); tokens != 300 {
			t.Fatalf("settleFirst=%v: the minute holds %d tokens, want the 300 used", settleFirst, tokens)
		}
		if !within(f.barUsed(t, bar), 0.5) || f.windowCredits(t, a) != 0 {
			t.Fatalf("settleFirst=%v: bar used %v, window %v", settleFirst, f.barUsed(t, bar), f.windowCredits(t, a))
		}
	}
}

// A bar that only pays part of a request leaves the rest to the window, and
// the per-minute limit still counts all of it.
func TestAPartlyPaidRequestCountsWholeTowardTheTokenRate(t *testing.T) {
	f := newBonusFixture(t, 10)
	f.rates(t, nil, ptrInt(10000), 10)
	a := f.person(t, "alice")
	f.bar(t, "small", bonus.KindBonus, bonus.ModeOn, 1, a)
	ctx := WithLedger(context.Background())

	res, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Tokens: 800, Credits: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, tokens := f.minute(t, a, WindowTPM); tokens != 800 {
		t.Fatalf("the minute holds %d tokens of an 800-token request", tokens)
	}
	if err := f.svc.Release(ctx, a.ID, res); err != nil {
		t.Fatal(err)
	}
	if _, tokens := f.minute(t, a, WindowTPM); tokens != 0 {
		t.Fatalf("released, the minute still holds %d tokens", tokens)
	}
}

// The same holds for a bar kept back and spent when the window is empty: it
// pays for the request, it does not exempt it from the rate.
func TestAFallbackBarIsHeldToTheRequestRateToo(t *testing.T) {
	f := newBonusFixture(t, 5)
	f.rates(t, ptrInt(3), nil, 5)
	a := f.person(t, "alice")
	off := f.bar(t, "kept back", bonus.KindBonus, bonus.ModeOff, 100, a)
	ctx := WithLedger(context.Background())

	if _, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 5}, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 1}, nil); err != nil {
			t.Fatalf("fallback request %d: %v", i+1, err)
		}
	}
	if requests, _ := f.minute(t, a, WindowRPM); requests != 3 {
		t.Fatalf("the minute counted %d requests of three", requests)
	}
	_, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Credits: 1}, nil)
	if exceeded, ok := AsExceeded(err); !ok || exceeded.Window != WindowRPM {
		t.Fatalf("the fourth request in a minute was not refused by the rate: %v", err)
	}
	if !within(f.barUsed(t, off), 2) {
		t.Fatalf("the bar carries %v, want the two requests that ran", f.barUsed(t, off))
	}
}

func TestAFallbackRequestGivesItsTokensBackToTheMinuteToo(t *testing.T) {
	f := newBonusFixture(t, 5)
	f.rates(t, nil, ptrInt(10000), 5)
	a := f.person(t, "alice")
	f.bar(t, "kept back", bonus.KindBonus, bonus.ModeOff, 100, a)
	ctx := WithLedger(context.Background())
	if _, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Tokens: 100, Credits: 5}, nil); err != nil {
		t.Fatal(err)
	}
	_, base := f.minute(t, a, WindowTPM)

	res, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Tokens: 400, Credits: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, tokens := f.minute(t, a, WindowTPM); tokens != base+400 {
		t.Fatalf("the minute holds %d, want %d", tokens, base+400)
	}
	if err := f.svc.Release(ctx, a.ID, res); err != nil {
		t.Fatal(err)
	}
	if _, tokens := f.minute(t, a, WindowTPM); tokens != base {
		t.Fatalf("released, the minute holds %d, want %d", tokens, base)
	}
}

// With no token limit in force nothing was put in the minute, so nothing is
// given back: a release must not write a negative figure where there was none.
func TestReleasingABarPaidRequestWritesNothingWhereNoTokenLimitApplies(t *testing.T) {
	f := newBonusFixture(t, 10)
	a := f.person(t, "alice")
	f.bar(t, "gift", bonus.KindBonus, bonus.ModeOn, 100, a)
	ctx := WithLedger(context.Background())
	res, err := f.svc.ReserveFor(ctx, a, "m", Estimate{Tokens: 500, Credits: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Release(ctx, a.ID, res); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := f.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM usage_counters WHERE scope_key = ? AND window_kind = ?`, scopeKey(a.ID), string(WindowTPM)).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("a per-minute row was written for an account with no token limit: %d", rows)
	}
}
