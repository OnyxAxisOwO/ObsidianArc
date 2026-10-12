package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/totp"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// An account opened through a provider has no password, so a session cookie is
// the only thing that comes with a request. Switching two-step sign-in on must
// not be possible from a session that signed in long ago: the rule for such an
// account is the same one ChangePassword and provider connect already apply.
func TestPasswordlessTwoStepNeedsASignInMadeJustNow(t *testing.T) {
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
	code := codeAt(t, setup.Secret, totp.Step(time.Now()))

	old := sessionFor(t, f, account.ID)
	ageSession(t, f, old.ID, 16*time.Minute)
	if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "", code, old.ID, "", ""); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("enabling from a sign-in made 16 minutes ago = %v, want ErrReauthRequired", err)
	}
	if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "", code, "", "", ""); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("enabling with no session presented = %v, want ErrReauthRequired", err)
	}
	if after, err := f.users.ByID(ctx, nil, account.ID); err != nil || after.TwoFactorEnabled() {
		t.Fatalf("a refused enable switched two-step on anyway: enabled=%t err=%v", after.TwoFactorEnabled(), err)
	}

	fresh := sessionFor(t, f, account.ID)
	if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "", code, fresh.ID, "", ""); err != nil {
		t.Fatalf("enabling from a sign-in made just now: %v", err)
	}
}

// Moving the address is where a reset link goes, so for a passwordless account
// it needs the same recent sign-in. The session that asks is the one on the
// request context, as the sign-in middleware puts it there.
func TestPasswordlessEmailChangeNeedsASignInMadeJustNow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, err := provision(t, f, ProvisionInput{Username: "octocat"})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	moved := "octocat@example.com"

	old := sessionFor(t, f, account.ID)
	ageSession(t, f, old.ID, 16*time.Minute)
	staleCtx := context.WithValue(ctx, sessionContextKey, old)
	if _, err := f.auth.UpdateProfile(staleCtx, account.ID, user.ProfileUpdate{Email: &moved}, ""); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("moving the address from a sign-in made 16 minutes ago = %v, want ErrReauthRequired", err)
	}
	if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &moved}, ""); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("moving the address with no session on the request = %v, want ErrReauthRequired", err)
	}
	if kept, err := f.users.ByID(ctx, nil, account.ID); err != nil || kept.Email != "" {
		t.Fatalf("a refused move changed the address to %q (%v)", kept.Email, err)
	}

	fresh := sessionFor(t, f, account.ID)
	freshCtx := context.WithValue(ctx, sessionContextKey, fresh)
	updated, err := f.auth.UpdateProfile(freshCtx, account.ID, user.ProfileUpdate{Email: &moved}, "")
	if err != nil {
		t.Fatalf("moving the address from a sign-in made just now: %v", err)
	}
	if updated.Email != moved {
		t.Fatalf("address = %q, want %q", updated.Email, moved)
	}
}

// A stale sign-in is the caller's to fix by signing in again, so it is a coded
// 403 the screens can word, not the server error an unmapped error becomes.
func TestAStaleSignInIsAForbiddenNotAServerError(t *testing.T) {
	for _, err := range []error{ErrReauthRequired} {
		var httpErr *httpx.Error
		w := httptest.NewRecorder()
		if !errors.As(TranslateTwoFactorError(w, err), &httpErr) {
			t.Fatalf("%v did not translate to an HTTP error", err)
		}
		if httpErr.Status != 403 || httpErr.Code != "reauth_required" {
			t.Fatalf("%v = %d %q, want 403 reauth_required", err, httpErr.Status, httpErr.Code)
		}
	}
}

// The current password is checked before the change's transaction starts. An
// administrator's reset that commits in between must not be overwritten by the
// change, which was made on the strength of the password the reset replaced.
func TestAPasswordChangeCannotOverwriteAResetMadeAfterItWasChecked(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}

	f.auth.beforeChangeWrite = func() {
		if err := f.auth.SetPassword(ctx, account.ID, "admin-reset-password"); err != nil {
			t.Errorf("the reset inside the window: %v", err)
		}
	}
	if _, err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", "a-better-password", ""); !errors.Is(err, ErrCurrentPasswordWrong) {
		t.Fatalf("a change checked against the replaced password = %v, want ErrCurrentPasswordWrong", err)
	}
	f.auth.beforeChangeWrite = nil

	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "admin-reset-password", IP: "198.51.100.21"}); err != nil {
		t.Fatalf("the administrator's password was overwritten: %v", err)
	}
	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-better-password", IP: "198.51.100.21"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("the password the change wrote opened the account: %v", err)
	}
}

// The same window for an address change: the password is checked, then the
// address is written in a later transaction. A reset landing between the two
// must stop the write, or the address moves on a password nobody holds any more.
func TestAnAddressChangeCannotOverwriteAResetMadeAfterItWasChecked(t *testing.T) {
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

	f.auth.beforeChangeWrite = func() {
		if err := f.auth.SetPassword(ctx, account.ID, "admin-reset-password"); err != nil {
			t.Errorf("the reset inside the window: %v", err)
		}
	}
	moved := "thief@example.com"
	if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &moved}, "a-good-password"); !errors.Is(err, ErrCurrentPasswordWrong) {
		t.Fatalf("moving the address on a replaced password = %v, want ErrCurrentPasswordWrong", err)
	}
	f.auth.beforeChangeWrite = nil

	kept, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Email != "member@example.com" {
		t.Fatalf("address = %q after a refused move, want it untouched", kept.Email)
	}
	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "member", Password: "admin-reset-password", IP: "198.51.100.22"}); err != nil {
		t.Fatalf("the administrator's password was overwritten: %v", err)
	}
}
