package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A link opened at the same moment its owner is changing address.
//
// Verify reads the token before the transaction that acts on it, so the
// deletion UpdateProfile performs to withdraw the link arrives too late to
// stop anything. While Verify also wrote the address onto the row, that made
// the stale link win: the change the owner had just been told was saved was
// undone, and the address they had left came back marked confirmed. Measured
// at the time, on this fixture: 3955 of 4000 attempts.
//
// Confirming instead of assigning is what closes it without a lock. The link
// matches on the address the account still has, so whichever transaction
// commits first, the other reads the value it would have changed:
//
//	Verify first          the move lands afterwards, unconfirmed
//	UpdateProfile first   no row matches, the link is spent and refused
//
// What must never appear is a row sitting at the old address, or at the new
// one marked confirmed.
func TestAStaleLinkNeverWinsAgainstAMoveInFlight(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	verifying(t, f)

	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "first", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}

	const rounds = 200
	moved, refused := 0, 0
	for i := range rounds {
		from := fmt.Sprintf("from%03d@example.com", i)
		to := fmt.Sprintf("to%03d@example.com", i)

		account, _, err := f.auth.Register(ctx, RegisterInput{
			Username: fmt.Sprintf("member%03d", i), Email: from, Password: "a-good-password",
		})
		if err != nil {
			t.Fatal(err)
		}
		token, err := f.auth.issueVerification(ctx, f.db, account.ID, from)
		if err != nil {
			t.Fatal(err)
		}

		gate := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-gate
			_, _ = f.auth.Verify(ctx, token)
		}()
		go func() {
			defer workers.Done()
			<-gate
			_, _ = f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &to})
		}()
		close(gate)
		workers.Wait()

		var email, lower string
		var confirmed bool
		if err := f.db.QueryRow(ctx,
			`SELECT email, email_lower, email_verified FROM users WHERE id = ?`, account.ID).
			Scan(&email, &lower, &confirmed); err != nil {
			t.Fatal(err)
		}

		if email == from {
			t.Fatalf("round %d: a stale link put the account back on %q", i, from)
		}
		if email != to {
			t.Fatalf("round %d: the account is at %q, which is neither address", i, email)
		}
		if lower != strings.ToLower(email) {
			t.Fatalf("round %d: email=%q and email_lower=%q disagree", i, email, lower)
		}
		if confirmed {
			t.Fatalf("round %d: %q is marked confirmed and nobody confirmed it", i, email)
		}
		moved++

		// Whichever way it went, the link is gone: leaving it open is what
		// would let it be opened again after the next move.
		var outstanding int
		if err := f.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM email_verifications WHERE id = ?`, digest(token)).Scan(&outstanding); err != nil {
			t.Fatal(err)
		}
		if outstanding != 0 {
			t.Fatalf("round %d: the stale link is still open", i)
		}
		refused++
	}

	if moved != rounds || refused != rounds {
		t.Fatalf("moved %d and spent %d of %d rounds", moved, refused, rounds)
	}
}
