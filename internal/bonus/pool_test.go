package bonus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

func (f *fixture) pool(t *testing.T, mode Mode, p Pool) Bar {
	t.Helper()
	return f.bar(t, Bar{Name: "shared", Kind: KindShared, ToggleMode: mode, DefaultOn: true, ShowTotal: true, Pool: &p})
}

func (f *fixture) poolRow(t *testing.T, barID string) Pool {
	t.Helper()
	p, err := f.store.pool(context.Background(), f.db, barID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAPoolKeepsItsRulesAndOnlyAPoolMayBeOnly(t *testing.T) {
	f := newFixture(t)
	if _, err := f.store.CreateBar(context.Background(), Bar{Name: "x", ToggleMode: ModeOnly}, ""); !errors.Is(err, ErrInvalidBar) {
		t.Errorf("a bar of one's own in 'only' mode: %v", err)
	}
	for _, bad := range []Pool{{ResetPeriod: "hour"}, {ResetPeriod: PeriodDay}, {PerUserDaily: -1}, {Amount: MaxAmount + 1}} {
		if _, err := f.store.CreateBar(context.Background(), Bar{Name: "x", Kind: KindShared, Pool: &bad}, ""); !errors.Is(err, ErrInvalidPool) {
			t.Errorf("%+v was accepted: %v", bad, err)
		}
	}
	bar := f.pool(t, ModeOnly, Pool{ResetPeriod: PeriodWeek, ResetAmount: 50, PerUserDaily: 3})
	if bar.Pool == nil || bar.Pool.Amount != 50 || bar.Pool.PerUserDaily != 3 || bar.Pool.PeriodStart == 0 {
		t.Fatalf("a weekly pool starts at its reset amount in this week: %+v", bar.Pool)
	}
}

func TestAPoolIsNotGrantedIntoNorTurnedIntoABar(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	bar := f.pool(t, ModeOn, Pool{Amount: 10})
	if _, err := f.store.Grant(context.Background(), bar.ID, Target{All: true}, 1, 0, "admin", "", ""); !errors.Is(err, ErrSharedNoGrants) {
		t.Errorf("grant into a pool: %v", err)
	}
	err := f.db.Tx(context.Background(), func(tx *database.Tx) error {
		_, err := f.store.GrantTo(context.Background(), tx, bar.ID, a.ID, 1, 0, "checkin", "")
		return err
	})
	if !errors.Is(err, ErrSharedNoGrants) {
		t.Errorf("a check-in grant into a pool: %v", err)
	}
	changed := bar
	changed.Kind = KindBonus
	if _, err := f.store.UpdateBar(context.Background(), nil, changed); !errors.Is(err, ErrKindChange) {
		t.Errorf("a pool turned into a bar: %v", err)
	}
	gift := f.bar(t, Bar{Name: "gift"})
	gift.Kind = KindShared
	if _, err := f.store.UpdateBar(context.Background(), nil, gift); !errors.Is(err, ErrKindChange) {
		t.Errorf("a bar turned into a pool: %v", err)
	}
}

func TestAPoolResetsWhenItsPeriodHasPassed(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	bar := f.pool(t, ModeOn, Pool{ResetPeriod: PeriodDay, ResetAmount: 10})
	holds, err := f.take(t, a.ID, "m", 4, Priority)
	if err != nil || len(holds) != 1 {
		t.Fatalf("take: %v %v", holds, err)
	}
	// Yesterday's balance, half spent and topped up: a reset puts it back to the
	// reset amount, not to what was there.
	if _, err := f.db.Exec(context.Background(), `UPDATE bonus_pools SET period_start = period_start - ?, amount = 30 WHERE bar_id = ?`,
		(24 * time.Hour).Milliseconds(), bar.ID); err != nil {
		t.Fatal(err)
	}
	// The hold was taken in that period too.
	holds[0].Period -= (24 * time.Hour).Milliseconds()
	view, err := f.store.Bar(context.Background(), nil, bar.ID)
	if err != nil || view.Pool.Amount != 10 || view.Pool.Used != 0 {
		t.Fatalf("a pool past its period reads as reset: %+v %v", view.Pool, err)
	}
	if _, err := f.take(t, a.ID, "m", 3, Priority); err != nil {
		t.Fatal(err)
	}
	if p := f.poolRow(t, bar.ID); p.Amount != 10 || !near(p.Used, 3) {
		t.Fatalf("after the reset: %+v", p)
	}
	// The hold from before the reset was wiped with its period, so giving it back
	// must not add to the balance that was just set.
	if err := f.store.Refund(context.Background(), f.db, holds); err != nil {
		t.Fatal(err)
	}
	if p := f.poolRow(t, bar.ID); !near(p.Used, 3) {
		t.Fatalf("a refund from the last period reached this one: used %v", p.Used)
	}
}

func TestATopUpAddsAndASetFixesWhatIsLeft(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	bar := f.pool(t, ModeOn, Pool{Amount: 10})
	if _, err := f.take(t, a.ID, "m", 4, Priority); err != nil {
		t.Fatal(err)
	}
	p, err := f.store.AdjustPool(context.Background(), bar.ID, false, 5)
	if err != nil || !near(p.Remaining(), 11) {
		t.Fatalf("add 5 to 6 left: %+v %v", p, err)
	}
	p, err = f.store.AdjustPool(context.Background(), bar.ID, false, -100)
	if err != nil || p.Remaining() != 0 || !near(p.Used, 4) {
		t.Fatalf("taking more than is left leaves nothing, and what was used stays: %+v %v", p, err)
	}
	p, err = f.store.AdjustPool(context.Background(), bar.ID, true, 7)
	if err != nil || !near(p.Remaining(), 7) {
		t.Fatalf("set 7: %+v %v", p, err)
	}
	if _, err := f.store.AdjustPool(context.Background(), bar.ID, true, -1); !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("set below zero: %v", err)
	}
	gift := f.bar(t, Bar{Name: "gift"})
	if _, err := f.store.AdjustPool(context.Background(), gift.ID, false, 1); !errors.Is(err, ErrNotShared) {
		t.Errorf("a top-up of a bar of one's own: %v", err)
	}
}

// Everybody sees a pool, holding a grant or not, and may switch an optional
// one without one.
func TestEveryAccountSeesAPoolAndMaySwitchAnOptionalOne(t *testing.T) {
	f := newFixture(t)
	a := f.account(t, "alice")
	bar := f.pool(t, ModeUser, Pool{Amount: 10, PerUserDaily: 4})
	if _, err := f.take(t, a.ID, "m", 1.5, Priority); err != nil {
		t.Fatal(err)
	}
	views, err := f.store.ViewFor(context.Background(), a.ID)
	if err != nil || len(views) != 1 {
		t.Fatalf("views: %+v %v", views, err)
	}
	v := views[0]
	if v.Kind != KindShared || !v.Choosable || !v.Enabled || v.Pool == nil || v.Pool.DailyLimit != 4 || !near(v.Pool.DailyUsed, 1.5) ||
		v.Remaining == nil || !near(*v.Remaining, 8.5) {
		t.Fatalf("view: %+v pool %+v", v, v.Pool)
	}
	if err := f.store.SetChoice(context.Background(), a.ID, bar.ID, false); err != nil {
		t.Fatal(err)
	}
	if holds, err := f.take(t, a.ID, "m", 1, Priority); err != nil || len(holds) != 0 {
		t.Fatalf("switched off, the pool still paid first: %v %v", holds, err)
	}
	forced := f.pool(t, ModeOnly, Pool{Amount: 1})
	if err := f.store.SetChoice(context.Background(), a.ID, forced.ID, false); !errors.Is(err, ErrNotChoosable) {
		t.Errorf("an only pool was switched off: %v", err)
	}
}

func TestOnlyAPoolInOnlyModeHoldsItsModels(t *testing.T) {
	f := newFixture(t)
	f.bar(t, Bar{Name: "shared", Kind: KindShared, ToggleMode: ModeOnly, ModelIDs: []string{"m"}, Pool: &Pool{Amount: 1}})
	f.bar(t, Bar{Name: "open", Kind: KindShared, ToggleMode: ModeOn, ModelIDs: []string{"n"}, Pool: &Pool{Amount: 1}})
	for model, want := range map[string]bool{"m": true, "n": false, "o": false} {
		if got, err := f.store.Exclusive(context.Background(), model); err != nil || got != want {
			t.Errorf("%s: %v %v, want %v", model, got, err, want)
		}
	}
}
