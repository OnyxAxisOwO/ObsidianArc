package quota

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
)

func TestResetDoesNotRefundAnOldReservationIntoNewCounters(t *testing.T) {
	service, _ := newServiceOn(t, dbtest.Either(t, filepath.Join(t.TempDir(), "quota.db")))
	ctx := context.Background()
	person := account("user-1", "")
	if _, err := service.Policies().Save(ctx, nil, Policy{Scope: ScopeGlobal, TPM: ptrInt(100000), Windows: map[Window]Limits{Window5H: limits(true, nil, ptrInt(100000), nil)}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old, err := service.reserve(ctx, person, "", Estimate{Tokens: 100, Credits: 1}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResetWindows(ctx, nil, []string{person.ID}, []string{string(Window5H), string(WindowTPM)}); err != nil {
		t.Fatal(err)
	}
	newer, err := service.reserve(ctx, person, "", Estimate{Tokens: 40, Credits: .4}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Release(ctx, person.ID, old); err != nil {
		t.Fatal(err)
	}
	for _, window := range []Window{Window5H, WindowTPM} {
		tokens, _ := counterAt(t, service, person.ID, window, now, old.anchor)
		if tokens != 40 {
			t.Errorf("%s tokens = %d, want post-reset reservation 40", window, tokens)
		}
	}
	// A non-reset allowance still refunds the old reservation normally.
	tokens, _ := counterAt(t, service, person.ID, WindowWeek, now, old.anchor)
	if tokens != 40 {
		t.Errorf("week tokens = %d, want 40", tokens)
	}
	if err := service.Release(ctx, person.ID, newer); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticResetDoesNotRefundAnOldReservationIntoNewCounter(t *testing.T) {
	service, db := newServiceOn(t, dbtest.Either(t, filepath.Join(t.TempDir(), "quota.db")))
	ctx := context.Background()
	person := account("user-1", "")
	if _, err := service.Policies().Save(ctx, nil, Policy{Scope: ScopeGlobal, Windows: map[Window]Limits{Window5H: limits(true, nil, ptrInt(100000), nil)}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old, err := service.reserve(ctx, person, "", Estimate{Tokens: 100, Credits: 1}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.db.Tx(ctx, func(tx *database.Tx) error {
		return clearAllowance(ctx, tx, scopeKey(person.ID), old.anchor, now, []string{string(Window5H)})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bump(ctx, db, scopeKey(person.ID), Window5H, bucketStart(Window5H, now, old.anchor), 1, 40, .4); err != nil {
		t.Fatal(err)
	}
	if err := service.Release(ctx, person.ID, old); err != nil {
		t.Fatal(err)
	}
	tokens, _ := counterAt(t, service, person.ID, Window5H, now, old.anchor)
	if tokens != 40 {
		t.Fatalf("post-reset tokens = %d, want 40", tokens)
	}
}
