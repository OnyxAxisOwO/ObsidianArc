package idp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// Consent is what an account has agreed to, and a later sign-in that asks for
// less must not take any of it away. Otherwise the application is asked again
// for a scope the person already gave.
func TestAnApprovalAskingForLessKeepsWhatWasAllowed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	app, _ := f.app(t, CreateAppInput{})
	wide := []string{ScopeOpenID, ScopeProfile, ScopeEmail}

	if err := f.store.RecordGrant(ctx, nil, app.ID, f.account.ID, wide); err != nil {
		t.Fatalf("record wide consent: %v", err)
	}
	if err := f.store.RecordGrant(ctx, nil, app.ID, f.account.ID, []string{ScopeOpenID}); err != nil {
		t.Fatalf("record narrow consent: %v", err)
	}
	stored, err := f.store.GrantedScopes(ctx, nil, app.ID, f.account.ID)
	if err != nil {
		t.Fatalf("read consent: %v", err)
	}
	if !Covers(stored, wide) {
		t.Errorf("stored consent = %v after a narrower approval, want it to keep %v", stored, wide)
	}
}

// RevokeGrant removes the grant before it revokes the tokens. A pair issued
// once the grant is gone would be live for an application the person withdrew,
// so issuance is refused while the grant is absent.
func TestATokenIsNotIssuedOnceTheGrantIsWithdrawn(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	app, _ := f.app(t, CreateAppInput{})
	if err := f.store.RecordGrant(ctx, nil, app.ID, f.account.ID, []string{ScopeOpenID}); err != nil {
		t.Fatalf("record consent: %v", err)
	}
	if removed, err := f.store.RevokeGrant(ctx, f.account.ID, app.ID); err != nil || !removed {
		t.Fatalf("withdraw = %v (%v), want the grant removed", removed, err)
	}

	_, err := f.store.SaveGrantedToken(ctx, "access-token", "refresh-token",
		app.ID, f.account.ID, []string{ScopeOpenID})
	if !errors.Is(err, ErrBadCode) {
		t.Fatalf("issuance after withdrawal = %v, want ErrBadCode", err)
	}
	var count int
	if err := f.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM oauth_tokens WHERE app_id = ?`, app.ID).Scan(&count); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if count != 0 {
		t.Errorf("%d token row(s) written for a withdrawn grant, want none", count)
	}
}

// Withdrawing an application while a code is being exchanged for it must end
// with no live pair. The withdrawal either sees the pair, or the issuance is
// refused. Without the grant lock, a pair inserted after the withdrawal had
// committed stayed live.
func TestWithdrawingAGrantRacesAnIssuanceWithoutLeavingALivePair(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	app, _ := f.app(t, CreateAppInput{})

	// A round in which the issuance lands first is the only kind that leaves a
	// pair for the withdrawal to revoke. The withdrawal usually wins, and with
	// 25 rounds some runs saw no issuance at all, so the rounds are many.
	// Counting them stops a refusal that always wins from passing the test with
	// nothing checked.
	var issued atomic.Int64
	for round := 0; round < 200; round++ {
		if err := f.store.RecordGrant(ctx, nil, app.ID, f.account.ID, []string{ScopeOpenID}); err != nil {
			t.Fatalf("round %d: record consent: %v", round, err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			// A refusal leaves no pair behind, which is the invariant under test,
			// so only a successful issuance is counted.
			if _, err := f.store.SaveGrantedToken(ctx, fmt.Sprintf("access-%d", round),
				fmt.Sprintf("refresh-%d", round), app.ID, f.account.ID, []string{ScopeOpenID}); err == nil {
				issued.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			// t.Fatal may not be called from another goroutine, so a failed
			// withdrawal is reported here and the grant check below still runs.
			if _, err := f.store.RevokeGrant(ctx, f.account.ID, app.ID); err != nil {
				t.Errorf("round %d: withdraw: %v", round, err)
			}
		}()
		wg.Wait()

		granted, err := f.store.GrantedScopes(ctx, nil, app.ID, f.account.ID)
		if err != nil {
			t.Fatalf("round %d: read consent: %v", round, err)
		}
		// The grant was recorded before the goroutines started, so nothing
		// but the withdrawal can have removed it.
		if len(granted) != 0 {
			t.Fatalf("round %d: consent %v survived the withdrawal", round, granted)
		}
		var live int
		if err := f.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM oauth_tokens WHERE app_id = ? AND user_id = ? AND revoked = ?`,
			app.ID, f.account.ID, false).Scan(&live); err != nil {
			t.Fatalf("round %d: count tokens: %v", round, err)
		}
		if live != 0 {
			t.Fatalf("round %d: %d live token(s) survived the withdrawal", round, live)
		}
	}
	if issued.Load() == 0 {
		t.Fatal("no round issued a pair, so no withdrawal was checked against one")
	}
}
