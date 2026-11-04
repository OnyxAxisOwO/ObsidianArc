package server

import (
	"errors"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The reset runs in the transaction that marks the card spent, so a spend that
// fails after the reset takes the reset back with it. A reset that committed on
// its own would restore an allowance for a card the failed spend left unspent.
func TestSpentCardResetRunsInTheSpendsTransaction(t *testing.T) {
	in := newInstance(t)
	reader := in.register("card-reader", "a-good-password")
	scope := "u:" + reader.userID
	now := time.Now().UnixMilli()
	if _, err := in.db.Exec(t.Context(),
		`INSERT INTO usage_counters (scope_key, window_kind, window_start, requests, tokens, credits)
		 VALUES (?, '5h', ?, 10, 500, 0.5)`, scope, now); err != nil {
		t.Fatal(err)
	}

	countRows := func() int {
		var left int
		if err := in.db.QueryRow(t.Context(),
			`SELECT COUNT(*) FROM usage_counters WHERE scope_key = ? AND window_kind = '5h'`,
			scope).Scan(&left); err != nil {
			t.Fatal(err)
		}
		return left
	}

	reset := cardSpentReset(in.server.quota)
	spend := func(failAfterReset bool) error {
		return in.db.Tx(t.Context(), func(tx *database.Tx) error {
			if err := reset(t.Context(), tx, user.User{ID: reader.userID}, card.Card{Windows: []string{"full"}}); err != nil {
				return err
			}
			if failAfterReset {
				return errors.New("the spend failed after the reset")
			}
			return nil
		})
	}

	if err := spend(true); err == nil {
		t.Fatal("the failing spend reported success")
	}
	if left := countRows(); left != 1 {
		t.Fatalf("a failed spend restored the allowance: %d rows left, want the 1 it had", left)
	}

	if err := spend(false); err != nil {
		t.Fatalf("spend with a reset: %v", err)
	}
	if left := countRows(); left != 0 {
		t.Fatalf("the allowance was not restored: %d rows left", left)
	}
}
