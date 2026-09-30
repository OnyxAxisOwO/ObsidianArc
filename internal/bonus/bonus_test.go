package bonus

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type fixture struct {
	store   *Store
	users   *user.Store
	db      *database.DB
	groupID string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "bonus.db"), MaxOpenConns: 8, MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	g, err := group.NewStore(db).Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{store: NewStore(db), users: user.NewStore(db), db: db, groupID: g.ID}
}

func (f *fixture) account(t *testing.T, name string) user.User {
	t.Helper()
	u, err := f.users.Create(context.Background(), nil, user.CreateInput{
		Username: name, PasswordHash: "x", Role: user.RoleUser, Status: user.StatusActive, GroupID: f.groupID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f *fixture) bar(t *testing.T, b Bar) Bar {
	t.Helper()
	b.DefaultOn = b.DefaultOn || b.ToggleMode == ""
	made, err := f.store.CreateBar(context.Background(), b, "test")
	if err != nil {
		t.Fatal(err)
	}
	return made
}

func (f *fixture) give(t *testing.T, barID, userID string, amount float64, expires int64) {
	t.Helper()
	if _, err := f.store.Grant(context.Background(), barID, Target{UserIDs: []string{userID}}, amount, expires, "admin", "", "test"); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) take(t *testing.T, userID, modelID string, want float64, phase Phase) ([]Hold, error) {
	t.Helper()
	var holds []Hold
	err := f.db.Tx(context.Background(), func(tx *database.Tx) error {
		var err error
		holds, err = f.store.Take(context.Background(), tx, userID, modelID, want, phase)
		return err
	})
	return holds, err
}

func (f *fixture) used(t *testing.T, barID, userID string) float64 {
	t.Helper()
	var used float64
	if err := f.db.QueryRow(context.Background(), `SELECT COALESCE(SUM(used), 0) FROM bonus_grants WHERE bar_id = ? AND user_id = ?`, barID, userID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	return used
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestABarNeedsANameAndAKindItKnows(t *testing.T) {
	f := newFixture(t)
	for _, bad := range []Bar{{Name: "  "}, {Name: "x", Kind: "loan"}, {Name: "x", ToggleMode: "sometimes"}} {
		if _, err := f.store.CreateBar(context.Background(), bad, ""); !errors.Is(err, ErrInvalidBar) {
			t.Errorf("%+v was accepted: %v", bad, err)
		}
	}
	reserve := f.bar(t, Bar{Name: "spare", Kind: KindReserve, ToggleMode: ModeOn})
	if reserve.ToggleMode != ModeOff {
		t.Errorf("a reserve has no switch and is last: mode %q", reserve.ToggleMode)
	}
}

func TestGrantsHaveLimitsOnAmountAndExpiry(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	bar := f.bar(t, Bar{Name: "gift", ToggleMode: ModeUser})
	for _, amount := range []float64{0, -1, MaxAmount + 1, math.NaN(), math.Inf(1)} {
		if _, err := f.store.Grant(context.Background(), bar.ID, Target{UserIDs: []string{a.ID}}, amount, 0, "admin", "", ""); !errors.Is(err, ErrInvalidAmount) {
			t.Errorf("amount %v: %v", amount, err)
		}
	}
	past := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := f.store.Grant(context.Background(), bar.ID, Target{UserIDs: []string{a.ID}}, 5, past, "admin", "", ""); !errors.Is(err, ErrInvalidExpiry) {
		t.Errorf("an expiry in the past: %v", err)
	}
}

func TestAGrantGoesToEveryoneAGroupOrNamedAccountsAndAllOrNothing(t *testing.T) {
	f := newFixture(t)
	a, b := f.account(t, "alice"), f.account(t, "bob")
	bar := f.bar(t, Bar{Name: "gift"})
	res, err := f.store.Grant(context.Background(), bar.ID, Target{All: true}, 3, 0, "admin", "hello", "test")
	if err != nil || res.Count != 2 {
		t.Fatalf("all: %+v %v", res, err)
	}
	res, err = f.store.Grant(context.Background(), bar.ID, Target{GroupID: a.GroupID}, 1, 0, "admin", "", "")
	if err != nil || res.Count != 2 {
		t.Fatalf("group: %+v %v", res, err)
	}
	// One name that does not exist stops the whole grant.
	before := f.used(t, bar.ID, b.ID)
	if _, err := f.store.Grant(context.Background(), bar.ID, Target{UserIDs: []string{a.ID, "nobody"}}, 9, 0, "admin", "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown account: %v", err)
	}
	var n int
	_ = f.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM bonus_grants WHERE user_id = ?`, a.ID).Scan(&n)
	if n != 2 || before != 0 {
		t.Fatalf("a failed grant left %d rows for alice", n)
	}
}

// What is spent first: the bars that are switched on, soonest expiry first;
// then, only when asked for as a fallback, the rest, reserves last.
func TestPriorityIsWhatIsSwitchedOnAndFallbackIsTheRestWithReservesLast(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	soon := time.Now().Add(24 * time.Hour).UnixMilli()
	later := time.Now().Add(72 * time.Hour).UnixMilli()
	forcedOn := f.bar(t, Bar{Name: "on", ToggleMode: ModeOn})
	chosen := f.bar(t, Bar{Name: "chosen", ToggleMode: ModeUser, DefaultOn: true})
	off := f.bar(t, Bar{Name: "off", ToggleMode: ModeOff})
	spare := f.bar(t, Bar{Name: "spare", Kind: KindReserve})
	f.give(t, forcedOn.ID, a.ID, 1, later)
	f.give(t, chosen.ID, a.ID, 1, soon)
	f.give(t, off.ID, a.ID, 1, 0)
	f.give(t, spare.ID, a.ID, 1, soon)

	holds, err := f.take(t, a.ID, "m1", 5, Priority)
	if err != nil || !near(Total(holds), 2) {
		t.Fatalf("priority took %v %v; want the two bars that are on", holds, err)
	}
	if f.used(t, off.ID, a.ID) != 0 || f.used(t, spare.ID, a.ID) != 0 {
		t.Fatal("a priority take reached a bar that is not switched on")
	}
	// Soonest expiry first: the chosen bar (soon) before the forced one (later).
	if len(holds) != 2 || !near(f.used(t, chosen.ID, a.ID), 1) {
		t.Fatalf("holds = %v", holds)
	}

	// Now everything switched on is empty; the fallback takes the forced-off
	// bar before the reserve, and takes it whole or not at all.
	if _, err := f.take(t, a.ID, "m1", 3, Fallback); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("a fallback that cannot be covered whole: %v", err)
	}
	if f.used(t, off.ID, a.ID) != 0 || f.used(t, spare.ID, a.ID) != 0 {
		t.Fatal("a fallback that failed kept what it had claimed")
	}
	holds, err = f.take(t, a.ID, "m1", 1.5, Fallback)
	if err != nil || !near(Total(holds), 1.5) {
		t.Fatalf("fallback: %v %v", holds, err)
	}
	if !near(f.used(t, off.ID, a.ID), 1) || !near(f.used(t, spare.ID, a.ID), 0.5) {
		t.Fatalf("the forced-off bar goes before the reserve: off %v spare %v", f.used(t, off.ID, a.ID), f.used(t, spare.ID, a.ID))
	}
}

func TestAUserBarTheAccountSwitchedOffWaitsForTheFallback(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	bar := f.bar(t, Bar{Name: "chosen", ToggleMode: ModeUser, DefaultOn: true})
	f.give(t, bar.ID, a.ID, 4, 0)
	if err := f.store.SetChoice(context.Background(), a.ID, bar.ID, false); err != nil {
		t.Fatal(err)
	}
	if holds, _ := f.take(t, a.ID, "m", 1, Priority); len(holds) != 0 {
		t.Fatalf("a bar switched off was spent first: %v", holds)
	}
	if holds, err := f.take(t, a.ID, "m", 1, Fallback); err != nil || !near(Total(holds), 1) {
		t.Fatalf("it is still there for the fallback: %v %v", holds, err)
	}
	if err := f.store.SetChoice(context.Background(), a.ID, bar.ID, true); err != nil {
		t.Fatal(err)
	}
	if holds, _ := f.take(t, a.ID, "m", 1, Priority); !near(Total(holds), 1) {
		t.Fatalf("switched back on: %v", holds)
	}
}

func TestOnlyABarTheAccountMaySwitchCanBeSwitched(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	forced := f.bar(t, Bar{Name: "forced", ToggleMode: ModeOn})
	f.give(t, forced.ID, a.ID, 1, 0)
	if err := f.store.SetChoice(context.Background(), a.ID, forced.ID, false); !errors.Is(err, ErrNotChoosable) {
		t.Errorf("a forced bar was switched: %v", err)
	}
	free := f.bar(t, Bar{Name: "free", ToggleMode: ModeUser})
	if err := f.store.SetChoice(context.Background(), a.ID, free.ID, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("a bar the account holds nothing in: %v", err)
	}
}

func TestABarOnlyCoversTheModelsItNames(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	bar := f.bar(t, Bar{Name: "gift", ToggleMode: ModeOn, ModelIDs: []string{"cheap"}})
	f.give(t, bar.ID, a.ID, 5, 0)
	if holds, _ := f.take(t, a.ID, "expensive", 1, Priority); len(holds) != 0 {
		t.Fatalf("spent on a model it does not cover: %v", holds)
	}
	if holds, _ := f.take(t, a.ID, "cheap", 1, Priority); !near(Total(holds), 1) {
		t.Fatalf("not spent on the model it covers: %v", holds)
	}
}

func TestExpiredInactiveAndRevokedGrantsAreNotSpent(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	bar := f.bar(t, Bar{Name: "gift", ToggleMode: ModeOn})
	f.give(t, bar.ID, a.ID, 2, time.Now().Add(time.Hour).UnixMilli())
	if _, err := f.db.Exec(context.Background(), `UPDATE bonus_grants SET expires_at = ?`, time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if holds, _ := f.take(t, a.ID, "m", 1, Priority); len(holds) != 0 {
		t.Fatalf("an expired grant was spent: %v", holds)
	}
	f.give(t, bar.ID, a.ID, 2, 0)
	bar.Active = false
	if _, err := f.store.UpdateBar(context.Background(), bar); err != nil {
		t.Fatal(err)
	}
	if holds, _ := f.take(t, a.ID, "m", 1, Priority); len(holds) != 0 {
		t.Fatalf("a bar that was switched off was spent: %v", holds)
	}
	bar.Active = true
	if _, err := f.store.UpdateBar(context.Background(), bar); err != nil {
		t.Fatal(err)
	}
	rows, _, _ := f.store.GrantsInBar(context.Background(), bar.ID, 10, 0)
	for _, r := range rows {
		if r.ExpiresAt == 0 {
			if got, err := f.store.Revoke(context.Background(), r.ID); err != nil || !near(got, 2) {
				t.Fatalf("revoke: %v %v", got, err)
			}
		}
	}
	if holds, _ := f.take(t, a.ID, "m", 1, Priority); len(holds) != 0 {
		t.Fatalf("a revoked grant was spent: %v", holds)
	}
}

// However many requests arrive together, what they take between them is what
// was granted and no more.
func TestAGrantCannotBeOverSpentByRequestsArrivingTogether(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	bar := f.bar(t, Bar{Name: "gift", ToggleMode: ModeOn})
	f.give(t, bar.ID, a.ID, 10, 0)

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		taken float64
	)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			holds, err := f.take(t, a.ID, "m", 1, Priority)
			if err != nil {
				return
			}
			mu.Lock()
			taken += Total(holds)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if taken > 10+1e-6 || !near(f.used(t, bar.ID, a.ID), taken) {
		t.Fatalf("24 requests took %v of a grant of 10 (recorded as %v)", taken, f.used(t, bar.ID, a.ID))
	}
	if taken < 10-1e-6 {
		t.Fatalf("requests that could have been covered were not: took %v of 10", taken)
	}
}

// Refunding puts back what was held, and never more than was spent.
func TestRefundPutsBackWhatWasHeldAndNeverBelowZero(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	bar := f.bar(t, Bar{Name: "gift", ToggleMode: ModeOn})
	f.give(t, bar.ID, a.ID, 4, 0)
	holds, _ := f.take(t, a.ID, "m", 3, Priority)
	if err := f.store.Refund(context.Background(), f.db, holds); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Refund(context.Background(), f.db, holds); err != nil {
		t.Fatal(err)
	}
	if f.used(t, bar.ID, a.ID) != 0 {
		t.Fatalf("used after a double refund: %v", f.used(t, bar.ID, a.ID))
	}
}

// Settling charges what the request cost to the grants that were held for it,
// and says what they did not cover; whether the hold has been given back yet
// must not change what the grant ends up carrying.
func TestSettleLeavesTheGrantCarryingTheCostWhicheverComesFirst(t *testing.T) {
	for _, releaseFirst := range []bool{true, false} {
		f := newFixture(t)
		a := f.account(t, "alice")
		bar := f.bar(t, Bar{Name: "gift", ToggleMode: ModeOn})
		f.give(t, bar.ID, a.ID, 5, 0)
		holds, _ := f.take(t, a.ID, "m", 2, Priority) // reserved 2
		ctx := context.Background()

		var covered float64
		var err error
		if releaseFirst {
			_ = f.store.Refund(ctx, f.db, holds)
			covered, err = f.store.Settle(ctx, f.db, holds, 1.5, true)
		} else {
			covered, err = f.store.Settle(ctx, f.db, holds, 1.5, false)
			_ = f.store.Refund(ctx, f.db, holds)
		}
		if err != nil || !near(covered, 1.5) || !near(f.used(t, bar.ID, a.ID), 1.5) {
			t.Fatalf("release first=%v: covered %v used %v err %v", releaseFirst, covered, f.used(t, bar.ID, a.ID), err)
		}
	}
}

func TestACostBeyondTheGrantIsLeftToTheWindows(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	bar := f.bar(t, Bar{Name: "gift", ToggleMode: ModeOn})
	f.give(t, bar.ID, a.ID, 1, 0)
	holds, _ := f.take(t, a.ID, "m", 3, Priority) // only 1 was there
	covered, err := f.store.Settle(context.Background(), f.db, holds, 2.5, false)
	_ = f.store.Refund(context.Background(), f.db, holds)
	if err != nil || !near(covered, 1) || !near(f.used(t, bar.ID, a.ID), 1) {
		t.Fatalf("covered %v used %v err %v", covered, f.used(t, bar.ID, a.ID), err)
	}
}

func TestTheViewShowsAmountsOnlyWhereTheBarSaysSo(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	shown := f.bar(t, Bar{Name: "shown", ToggleMode: ModeUser, ShowTotal: true, DefaultOn: true})
	hidden := f.bar(t, Bar{Name: "hidden", ToggleMode: ModeOn, ShowTotal: false})
	// The one that expires first is spent first, which is what makes it the one
	// the take below reaches.
	f.give(t, shown.ID, a.ID, 1000, time.Now().Add(48*time.Hour).UnixMilli())
	f.give(t, hidden.ID, a.ID, 1000, 0)
	if _, err := f.take(t, a.ID, "m", 250, Priority); err != nil {
		t.Fatal(err)
	}
	views, err := f.store.ViewFor(context.Background(), a.ID)
	if err != nil || len(views) != 2 {
		t.Fatalf("views: %+v %v", views, err)
	}
	for _, v := range views {
		switch v.Name {
		case "shown":
			if v.Total == nil || *v.Total != 1000 || v.Remaining == nil || !near(*v.Remaining, 750) || v.Percent != 75 || !v.Choosable {
				t.Errorf("shown = %+v", v)
			}
		case "hidden":
			if v.Total != nil || v.Remaining != nil || v.Percent != 100 || v.Choosable || !v.Enabled {
				t.Errorf("hidden = %+v", v)
			}
		}
	}
}

// A person is told once that something is about to lapse, and only about what
// they still have.
func TestAnAccountIsToldOnceWhatIsAboutToExpire(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	bar := f.bar(t, Bar{Name: "gift", ToggleMode: ModeOn})
	soon := time.Now().Add(48 * time.Hour).UnixMilli()
	f.give(t, bar.ID, a.ID, 4, soon)
	f.give(t, bar.ID, a.ID, 1, soon)
	f.give(t, bar.ID, a.ID, 9, time.Now().Add(30*24*time.Hour).UnixMilli()) // not soon
	f.give(t, bar.ID, a.ID, 7, 0)                                           // never

	got, err := f.store.TakeExpiring(context.Background(), 72*time.Hour)
	if err != nil || len(got) != 1 || !near(got[0].Remaining, 5) || got[0].BarName != "gift" {
		t.Fatalf("expiring: %+v %v", got, err)
	}
	if again, _ := f.store.TakeExpiring(context.Background(), 72*time.Hour); len(again) != 0 {
		t.Fatalf("told twice: %+v", again)
	}
}
