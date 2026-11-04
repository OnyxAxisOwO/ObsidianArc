package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The console needs to know whether a password is right without starting a
// browser session, so the one thing worth pinning is that it does not leave
// one behind.
func TestVerifyCredentialAuthenticatesWithoutIssuingASession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	before := sessionCount(t, f)

	account, _, err := f.auth.VerifyCredential(ctx, "ARC", "a-good-password", "198.51.100.7")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if account.Username != "arc" {
		t.Fatalf("verified %q, want arc", account.Username)
	}
	if after := sessionCount(t, f); after != before {
		t.Errorf("sessions went from %d to %d; the console must not open one", before, after)
	}
}

func TestVerifyCredentialRejectsWrongPasswordAndUnknownAccountIdentically(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}

	_, _, wrongPassword := f.auth.VerifyCredential(ctx, "arc", "not-it-at-all", "198.51.100.7")
	_, _, unknownAccount := f.auth.VerifyCredential(ctx, "nobody", "not-it-at-all", "198.51.100.8")

	if !errors.Is(wrongPassword, ErrInvalidCredentials) || !errors.Is(unknownAccount, ErrInvalidCredentials) {
		t.Fatalf("errors differ: wrong password %v, unknown account %v", wrongPassword, unknownAccount)
	}
	// An SSH client shows the server's refusal to whoever typed it, so the
	// two must not be distinguishable there either.
	if wrongPassword.Error() != unknownAccount.Error() {
		t.Errorf("messages differ:\n %q\n %q", wrongPassword, unknownAccount)
	}
}

func TestVerifyCredentialRefusesADisabledAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	disabled := user.StatusDisabled
	if _, err := f.users.UpdateAdminFields(ctx, nil, account.ID, user.AdminUpdate{Status: &disabled}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if _, _, err := f.auth.VerifyCredential(ctx, "arc", "a-good-password", "198.51.100.7"); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("verify a disabled account = %v, want ErrAccountDisabled", err)
	}
}

// Guessing over SSH and guessing at the sign-in form spend one budget, not
// two. A separate limiter for the console would be a way round the one that
// already exists.
func TestVerifyCredentialSharesTheLoginAttemptBudget(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}

	const address = "198.51.100.9"
	for attempt := 0; attempt <= freeAttempts; attempt++ {
		if _, _, err := f.auth.VerifyCredential(ctx, "arc", "wrong-password-here", address); err != nil {
			var limited *RateLimitError
			if errors.As(err, &limited) {
				break
			}
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("attempt %d: %v", attempt, err)
			}
		}
	}

	_, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password", IP: address})
	var limited *RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("login after the console burned the budget = %v, want a rate limit", err)
	}
}

// The gate exists to keep scripts off the sign-in form. No SSH client can
// solve it, so requiring one there would only mean nobody could connect.
func TestVerifyCredentialIsNotHeldToTheBrowserChallenge(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	f.auth.LoginChallenge = turnstile.Gate{
		Enabled: func() bool { return true },
		Secret:  func() string { return "test-secret" },
	}

	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"}); !errors.Is(err, turnstile.ErrFailed) {
		t.Fatalf("the browser login should still be gated, got %v", err)
	}
	if _, _, err := f.auth.VerifyCredential(ctx, "arc", "a-good-password", "198.51.100.7"); err != nil {
		t.Fatalf("console verification should not be gated, got %v", err)
	}
}

func sessionCount(t *testing.T, f *fixture) int {
	t.Helper()
	var count int
	if err := f.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return count
}
