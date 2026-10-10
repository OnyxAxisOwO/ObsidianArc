package auth

import (
	"context"
	"errors"
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

// The fingerprint a check returns is the one of the hash it checked, so a
// password change after the check leaves the connection holding a value the
// account no longer has. Read as a second step, the value would be the new one.
func TestVerifyCredentialReturnsTheFingerprintOfTheHashItChecked(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.auth.CredentialFingerprint(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}

	_, checked, err := f.auth.VerifyCredential(ctx, "arc", "a-good-password", "198.51.100.7")
	if err != nil {
		t.Fatal(err)
	}
	if checked != before {
		t.Fatal("the fingerprint from the check is not the one the account reports before any change")
	}

	// The password is changed between reading the stored hash and checking it.
	// The check still passes, because it runs against the hash that was read, so
	// the fingerprint has to be that hash's, not the one a second read would find.
	f.auth.afterCredentialRead = func() {
		if err := f.auth.SetPassword(ctx, account.ID, "a-better-password"); err != nil {
			t.Error(err)
		}
	}
	_, checked, err = f.auth.VerifyCredential(ctx, "arc", "a-good-password", "198.51.100.7")
	if err != nil {
		t.Fatal(err)
	}
	if checked != before {
		t.Fatal("the fingerprint returned for a password changed during the check is not the one of the hash that was checked")
	}

	now, err := f.auth.CredentialFingerprint(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if now == checked {
		t.Fatal("the change made during the check did not land, so this test proves nothing")
	}
}

// A sign-in that rehashes the stored hash returns the fingerprint of the hash it
// checked. The rehash has moved the stored hash on by the time the check returns,
// so the connection that sign-in opens ends at its first command, once.
func TestVerifyCredentialOfARehashedPasswordReturnsTheOldFingerprint(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	stale := testParams()
	stale.Memory /= 2
	old, err := NewHasher(stale).Hash(ctx, "a-good-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.users.SetPasswordHash(ctx, nil, account.ID, old); err != nil {
		t.Fatal(err)
	}

	_, checked, err := f.auth.VerifyCredential(ctx, "arc", "a-good-password", "198.51.100.7")
	if err != nil {
		t.Fatal(err)
	}
	if checked != fingerprintOf(old) {
		t.Fatal("the check did not return the fingerprint of the hash it checked")
	}
	now, err := f.auth.CredentialFingerprint(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if now == checked {
		t.Fatal("the rehash left the stored hash where it was, so this test does not exercise the rehash")
	}
}

// The rehash is computed after the password is checked, so a change made in
// between is stored by the time it writes. Writing its hash unconditionally put
// the old password back over that change. The change is made from inside the
// window the rehash computes in, which is the only point the race can happen.
func TestARehashNeverPutsBackAPasswordChangedAfterTheCheck(t *testing.T) {
	signIns := map[string]func(f *fixture) error{
		"sign-in": func(f *fixture) error {
			_, _, err := f.auth.Login(context.Background(), LoginInput{Identifier: "arc", Password: "a-good-password", IP: "198.51.100.7"})
			return err
		},
		"console": func(f *fixture) error {
			_, _, err := f.auth.VerifyCredential(context.Background(), "arc", "a-good-password", "198.51.100.7")
			return err
		},
	}
	for name, signIn := range signIns {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			account, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
			if err != nil {
				t.Fatal(err)
			}
			stale := testParams()
			stale.Memory /= 2
			old, err := NewHasher(stale).Hash(ctx, "a-good-password")
			if err != nil {
				t.Fatal(err)
			}
			if err := f.users.SetPasswordHash(ctx, nil, account.ID, old); err != nil {
				t.Fatal(err)
			}

			reached := false
			f.auth.beforeRehashWrite = func() {
				reached = true
				if err := f.auth.SetPassword(ctx, account.ID, "a-better-password"); err != nil {
					t.Errorf("changing the password inside the rehash window: %v", err)
				}
			}
			if err := signIn(f); err != nil {
				t.Fatalf("the sign-in with the right password: %v", err)
			}
			if !reached {
				t.Fatal("the sign-in never reached the rehash, so this test does not exercise it")
			}

			if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-better-password", IP: "198.51.100.8"}); err != nil {
				t.Fatalf("the new password was refused after the rehash: %v", err)
			}
			if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password", IP: "198.51.100.8"}); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("the old password opened the account after it was changed: %v", err)
			}
		})
	}
}
