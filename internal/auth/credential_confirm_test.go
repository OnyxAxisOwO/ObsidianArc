package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/totp"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A stolen session is a session and nothing else. If it could switch the
// second step on with the thief's own authenticator, it would sign the owner
// out everywhere and be the only way back in.
func TestEnablingTwoFactorNeedsThePassword(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, session, _ := f.auth.Authenticate(ctx, token)
	setup, err := f.auth.BeginTwoFactor(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	if !setup.PasswordRequired {
		t.Fatal("the wizard was not told to ask for the password")
	}
	code := codeAt(t, setup.Secret, totp.Step(time.Now()))

	if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "", code, session.ID, "", ""); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("enabling with no password = %v, want it asked for", err)
	}
	if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "not-the-password", code, session.ID, "", ""); !errors.Is(err, ErrCurrentPasswordWrong) {
		t.Fatalf("enabling with a wrong password = %v, want a refusal", err)
	}
	after, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TwoFactorEnabled() {
		t.Fatal("the second step was switched on without the password")
	}

	if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "a-good-password", code, session.ID, "", ""); err != nil {
		t.Fatalf("enabling with the password: %v", err)
	}
}

// An account that signs in through a provider has no password, and asking it
// for one would lock it out of two-step sign-in for good.
func TestAPasswordlessAccountEnablesTwoFactorWithoutOne(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, err := provision(t, f, ProvisionInput{Username: "octocat"})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	setup, err := f.auth.BeginTwoFactor(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	if setup.PasswordRequired {
		t.Fatal("the wizard would ask an account with no password for one")
	}
	code := codeAt(t, setup.Secret, totp.Step(time.Now()))
	if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "", code, "", "", ""); err != nil {
		t.Fatalf("enabling on a passwordless account: %v", err)
	}
}

// The address is where a reset link goes, so repointing it is as good as
// taking the account.
func TestMovingTheEmailNeedsThePassword(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "first", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "member", Email: "member@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}

	moved := "thief@example.com"
	if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &moved}, ""); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("moving the address with no password = %v, want it asked for", err)
	}
	if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &moved}, "not-the-password"); !errors.Is(err, ErrCurrentPasswordWrong) {
		t.Fatalf("moving the address with a wrong password = %v, want a refusal", err)
	}
	kept, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Email != "member@example.com" {
		t.Fatalf("address = %q after refusals, want it untouched", kept.Email)
	}

	// Everything that is not the address stays free: the profile form sends
	// the unchanged address with every save.
	same, nickname := "Member@Example.com", "Nick"
	if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &same, Nickname: &nickname}, ""); err != nil {
		t.Fatalf("saving a nickname with the address unchanged: %v", err)
	}

	updated, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &moved}, "a-good-password")
	if err != nil {
		t.Fatalf("moving the address with the password: %v", err)
	}
	if updated.Email != moved {
		t.Fatalf("address = %q, want %q", updated.Email, moved)
	}
}

func TestAPasswordlessAccountMovesItsEmailWithoutOne(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, err := provision(t, f, ProvisionInput{Username: "octocat"})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	moved := "octocat@example.com"
	if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &moved}, ""); err != nil {
		t.Fatalf("moving the address on a passwordless account: %v", err)
	}
}

// The password prompt must not become the guessing oracle the sign-in form
// was throttled to avoid: past the free attempts even the right password
// waits, or the throttle would say the moment a guess was right.
func TestConfirmingThePasswordIsThrottled(t *testing.T) {
	cases := map[string]func(t *testing.T, f *fixture, account user.User, password string) error{
		"enabling two-step": func(t *testing.T, f *fixture, account user.User, password string) error {
			_, _, err := f.auth.EnableTwoFactor(context.Background(), account.ID, password, "000000", "", "", "")
			return err
		},
		"moving the address": func(t *testing.T, f *fixture, account user.User, password string) error {
			moved := "other@example.com"
			_, err := f.auth.UpdateProfile(context.Background(), account.ID, user.ProfileUpdate{Email: &moved}, password)
			return err
		},
		"changing the password": func(t *testing.T, f *fixture, account user.User, password string) error {
			_, err := f.auth.ChangePassword(context.Background(), account.ID, password, "another-good-password", "")
			return err
		},
	}
	for name, attempt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "first", Password: "a-good-password"}); err != nil {
				t.Fatal(err)
			}
			account, _, err := f.auth.Register(ctx, RegisterInput{
				Username: "member", Email: "member@example.com", Password: "a-good-password",
			})
			if err != nil {
				t.Fatal(err)
			}

			for i := 0; i < freeAttempts; i++ {
				if err := attempt(t, f, account, "guess"); !errors.Is(err, ErrCurrentPasswordWrong) {
					t.Fatalf("guess %d = %v, want the plain refusal", i, err)
				}
			}
			// The first refusal after the free attempts is still a wrong
			// password; the one after it is made to wait.
			_ = attempt(t, f, account, "guess")
			var limited *RateLimitError
			if err := attempt(t, f, account, "a-good-password"); !errors.As(err, &limited) {
				t.Fatalf("the right password went straight through a throttled account: %v", err)
			}
		})
	}
}

// The allowance is taken before the password is hashed, so guesses sent all at
// once get the same few tries that guesses sent one after another do — not a
// wave that all pass the gate before the first one has failed.
func TestParallelPasswordGuessesGetTheSameAllowanceAsSequentialOnes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}

	const guesses = 24
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		refused int
		wrong   int
		other   []error
	)
	gate := make(chan struct{})
	for i := 0; i < guesses; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			err := f.auth.ConfirmPassword(ctx, account.ID, "guess")
			var limited *RateLimitError
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.As(err, &limited):
				refused++
			case errors.Is(err, ErrCurrentPasswordWrong):
				wrong++
			default:
				other = append(other, err)
			}
		}()
	}
	close(gate)
	wg.Wait()

	if len(other) != 0 {
		t.Fatalf("unexpected results: %v", other)
	}
	// Five free tries, and the sixth is let through to fail and start the wait.
	if wrong > freeAttempts+1 || wrong+refused != guesses {
		t.Fatalf("%d guesses reached the hash and %d were held back, of %d sent at once", wrong, refused, guesses)
	}
}

// Guessing a password spends its own allowance, so a flood of wrong ones
// cannot be used to lock an owner out of their authenticator's codes.
func TestPasswordGuessesDoNotSpendTheCodeAllowance(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, secret, _, used := enrolled(t, f, "arc")

	for i := 0; i < freeAttempts+2; i++ {
		_, _ = f.auth.ChangePassword(ctx, account.ID, "guess", "another-good-password", "")
	}
	if err := f.auth.VerifyTwoFactorCode(ctx, account, codeAt(t, secret, used+1), "198.51.100.4"); err != nil {
		t.Fatalf("a right code was refused after password guesses: %v", err)
	}
}
