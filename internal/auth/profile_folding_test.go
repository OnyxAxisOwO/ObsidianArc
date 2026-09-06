package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Whether the address moved is the gate everything else in the profile form
// hangs on: the allowlist, the withdrawal, the outstanding links. It used to
// be decided with EqualFold, which applies Unicode simple case folding, while
// the identity it guards is written with ToLower, which does not.
//
// U+017F, the long s, is where they part. EqualFold reads "boſs" and "boss" as
// one word; ToLower leaves U+017F alone. So the gate said nothing had moved
// while the store went on to write a different email_lower — a different
// account, as far as login and the uniqueness index are concerned.
func TestTheMoveIsDecidedInTheAlphabetTheIdentityIsWrittenIn(t *testing.T) {
	const longS = "ſ"
	if !strings.EqualFold("boss", "bo"+longS+"s") {
		t.Skip("this Go release does not fold U+017F; the test has nothing to catch")
	}
	if strings.ToLower("bo"+longS+"s") == "boss" {
		t.Skip("this Go release lowercases U+017F; the two functions agree")
	}

	t.Run("the allowlist is not walked past", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		if err := f.settings.Set(ctx, settings.EmailDomains, "sina.com"); err != nil {
			t.Fatal(err)
		}

		account, _, err := f.auth.Register(ctx, RegisterInput{
			Username: "founder", Email: "me@sina.com", Password: "a-good-password",
		})
		if err != nil {
			t.Fatal(err)
		}

		// The obvious way in is refused, as it always was.
		if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{
			Email: ptr("me@evil.test"),
		}); err == nil {
			t.Fatal("an address outside the allowlist was accepted")
		}

		// And so is the one that folds to an allowed domain without being one.
		disguised := "me@" + longS + "ina.com"
		if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{
			Email: &disguised,
		}); err == nil {
			t.Fatalf("%q was accepted against an allowlist of sina.com", disguised)
		}
	})

	t.Run("a fold that changes the identity is a move", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		verifying(t, f)

		if _, _, err := f.auth.Register(ctx, RegisterInput{
			Username: "first", Password: "a-good-password",
		}); err != nil {
			t.Fatal(err)
		}
		account, _, err := f.auth.Register(ctx, RegisterInput{
			Username: "founder", Email: "boss@example.com", Password: "a-good-password",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.auth.Verify(ctx, mustIssue(t, f, account.ID, "boss@example.com")); err != nil {
			t.Fatal(err)
		}

		disguised := "bo" + longS + "s@example.com"
		if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &disguised}); err != nil {
			t.Fatalf("the change was refused outright: %v", err)
		}

		var lower string
		var confirmed bool
		if err := f.db.QueryRow(ctx,
			`SELECT email_lower, email_verified FROM users WHERE id = ?`, account.ID).
			Scan(&lower, &confirmed); err != nil {
			t.Fatal(err)
		}
		if lower == "boss@example.com" {
			t.Fatal("the store wrote the old identity; this test is not exercising the fold")
		}
		if confirmed {
			t.Errorf("email_lower moved to %q and the account is still marked confirmed", lower)
		}
	})
}

func mustIssue(t *testing.T, f *fixture, userID, address string) string {
	t.Helper()
	token, err := f.auth.issueVerification(context.Background(), f.db, userID, address)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// An account with no address has nothing to confirm and nothing to hold back —
// the rule user.Store.Create keeps with `!in.Unverified || email == ""`.
//
// Clearing the address broke it: the account was marked unconfirmed, a link
// was issued for the empty string and posted there, and the resend that would
// have been the way out refuses when there is no address. The owner was shut
// out of sending anything until they typed one back in, and the settings form
// makes that one keystroke away.
func TestClearingAnAddressDoesNotStrandTheAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	verifying(t, f)

	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "first", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "member@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}

	cleared, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: ptr("")})
	if err != nil {
		t.Fatalf("clearing the address was refused: %v", err)
	}
	if cleared.Email != "" {
		t.Fatalf("address = %q, want it cleared", cleared.Email)
	}
	if !cleared.EmailVerified {
		t.Error("an account with no address is held back for a confirmation it cannot give")
	}

	var outstanding int
	if err := f.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM email_verifications WHERE user_id = ?`, account.ID).Scan(&outstanding); err != nil {
		t.Fatal(err)
	}
	if outstanding != 0 {
		t.Errorf("%d links outstanding for an account with nowhere to send one", outstanding)
	}
}

// The profile form posts mail. The resend button posts mail too, and has a
// throttle with a comment saying why: without it, it is a way to have this
// server post to a stranger repeatedly. The default allowlist is empty, so the
// stranger can be anyone.
//
// The limit is on the posting rather than on the move, because registration
// posts a link of its own — refusing the change would mean nobody could
// correct an address they had just mistyped into the sign-up form.
func TestMovingAddressCannotPostMailFasterThanResendWould(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	verifying(t, f)

	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "first", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "member@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}

	var firstIssued int64
	if err := f.db.QueryRow(ctx,
		`SELECT created_at FROM email_verifications WHERE user_id = ?`, account.ID).Scan(&firstIssued); err != nil {
		t.Fatalf("registration issued no link: %v", err)
	}

	// Every one of these is accepted — the owner may correct a typo — but the
	// window they all share is the one registration opened.
	for i, address := range []string{
		"stranger1@somewhere.test", "stranger2@somewhere.test", "stranger3@somewhere.test",
	} {
		if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &address}); err != nil {
			t.Fatalf("move %d refused: %v", i, err)
		}

		var issued int64
		var addressOnLink string
		if err := f.db.QueryRow(ctx,
			`SELECT created_at, email FROM email_verifications WHERE user_id = ?`, account.ID).
			Scan(&issued, &addressOnLink); err != nil {
			t.Fatalf("move %d left no link: %v", i, err)
		}
		// The link is always the new address's — an old one must not survive.
		if addressOnLink != address {
			t.Errorf("move %d left a link for %q, want %q", i, addressOnLink, address)
		}
		// And it carries the original clock, so moving is not a way to reset
		// the resend limit and post again immediately.
		if issued != firstIssued {
			t.Errorf("move %d reset the window from %d to %d", i, firstIssued, issued)
		}
	}

	// Which is what the resend button reads, so it is still shut too.
	if err := f.auth.Resend(ctx, "Arc", account.ID); err != ErrResendTooSoon {
		t.Errorf("resend returned %v, want ErrResendTooSoon", err)
	}
}
