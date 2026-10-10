package auth

import (
	"context"
	"testing"
)

// A console connection holds the fingerprint it was opened with and ends when
// it changes, so it must move with the password and with nothing else: a
// sign-in leaves the password where it was, and an administrator's reset or the
// owner's own change must move it.
func TestCredentialFingerprintMovesWithThePasswordAndNothingElse(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := func() string {
		t.Helper()
		value, err := f.auth.CredentialFingerprint(ctx, account.ID)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	before := fingerprint()
	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	if got := fingerprint(); got != before {
		t.Fatal("a sign-in moved the fingerprint, which would end every console connection on each sign-in")
	}

	if _, err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", "a-better-password", ""); err != nil {
		t.Fatal(err)
	}
	afterChange := fingerprint()
	if afterChange == before {
		t.Fatal("the owner's password change did not move the fingerprint")
	}

	if err := f.auth.SetPassword(ctx, account.ID, "an-administrators-reset"); err != nil {
		t.Fatal(err)
	}
	if afterReset := fingerprint(); afterReset == afterChange {
		t.Fatal("an administrator's password reset did not move the fingerprint")
	}
}
