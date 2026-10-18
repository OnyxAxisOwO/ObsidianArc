package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
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

	f.auth.hooks.beforeChangeWrite = func() {
		if err := f.auth.SetPassword(ctx, account.ID, "admin-reset-password"); err != nil {
			t.Errorf("the reset inside the window: %v", err)
		}
	}
	if _, err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", "a-better-password", ""); !errors.Is(err, ErrCurrentPasswordWrong) {
		t.Fatalf("a change checked against the replaced password = %v, want ErrCurrentPasswordWrong", err)
	}
	f.auth.hooks.beforeChangeWrite = nil

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

	f.auth.hooks.beforeChangeWrite = func() {
		if err := f.auth.SetPassword(ctx, account.ID, "admin-reset-password"); err != nil {
			t.Errorf("the reset inside the window: %v", err)
		}
	}
	moved := "thief@example.com"
	if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &moved}, "a-good-password"); !errors.Is(err, ErrCurrentPasswordWrong) {
		t.Fatalf("moving the address on a replaced password = %v, want ErrCurrentPasswordWrong", err)
	}
	f.auth.hooks.beforeChangeWrite = nil

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

// An IPv6 address is one subscriber's /64, which is how the login and proof-of-
// work limiters already count it. A per-address sign-up limit that counted each
// full address would let one subscriber make as many accounts as it has
// addresses, so every address in the block shares one count.
func TestTheSignupLimitCountsAnIPv6BlockAsOneAddress(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Password: "a-good-password", IP: "203.0.113.7",
	}); err != nil {
		t.Fatal(err)
	}
	set(t, f, settings.RegistrationEnabled, "true")
	set(t, f, settings.SignupsPerIP, "2")
	set(t, f, settings.SignupsIPWindowMin, "60")

	for i, ip := range []string{"2001:db8:1::10", "2001:db8:1::20"} {
		if _, _, err := f.auth.Register(ctx, RegisterInput{
			Username: names[i], Password: "a-good-password", IP: ip,
		}); err != nil {
			t.Fatalf("account %d from %s was refused: %v", i+1, ip, err)
		}
	}
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "charlie", Password: "a-good-password", IP: "2001:db8:1::30",
	}); !errors.Is(err, ErrSignupIPBlocked) {
		t.Fatalf("a third address in the same /64 gave %v, want ErrSignupIPBlocked", err)
	}
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "delta", Password: "a-good-password", IP: "2001:db8:2::10",
	}); err != nil {
		t.Errorf("an address in another /64 was caught by the first block's limit: %v", err)
	}
}

// Each address an account moves to is one paid lookup, and the account's own
// password is the only thing asked for. Without a limit per account, one signed-in
// account can spend the instance's lookup allowance and refuse every sign-up
// that screens an address after it.
func TestAnAccountCannotSpendTheScreeningAllowanceWithoutLimit(t *testing.T) {
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
	lookups := 0
	f.auth.Hooks.ScreenEmail = func(context.Context, string) error {
		lookups++
		return nil
	}
	f.auth.Hooks.ScreenEmailCharges = func(string) bool { return true }

	for i := 0; i < emailScreensPerHour; i++ {
		address := fmt.Sprintf("member%d@example.com", i)
		if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &address}, "a-good-password"); err != nil {
			t.Fatalf("move %d: %v", i+1, err)
		}
	}
	over := "member-over@example.com"
	var limited *RateLimitError
	if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &over}, "a-good-password"); !errors.As(err, &limited) {
		t.Fatalf("move %d = %v, want a RateLimitError", emailScreensPerHour+1, err)
	}
	if lookups != emailScreensPerHour {
		t.Fatalf("%d lookups for %d moves: a refused move must not reach the provider", lookups, emailScreensPerHour)
	}
	if kept, err := f.users.ByID(ctx, nil, account.ID); err != nil || kept.Email == over {
		t.Fatalf("the refused move changed the address to %q (%v)", kept.Email, err)
	}

	other, _, err := f.auth.Register(ctx, RegisterInput{Username: "other", Email: "other@example.com", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := "other-moved@example.com"
	if _, err := f.auth.UpdateProfile(ctx, other.ID, user.ProfileUpdate{Email: &elsewhere}, "a-good-password"); err != nil {
		t.Errorf("another account was held to the first one's allowance: %v", err)
	}
}

// The sign-up limit is instance-wide, and an instance may run two processes
// against one database. Each process must count the sign-ups the other one has
// made, or the limit is multiplied by the number of processes. The founder's
// sign-up counts too, as every account does.
func TestTheSignupRateLimitHoldsAcrossProcessesSharingADatabase(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	set(t, f, settings.RegistrationEnabled, "true")
	set(t, f, settings.SignupsPerMinute, "3")

	cfg := config.Config{
		Password:  testParams(),
		Session:   config.Session{TTL: time.Hour, CookieName: "obsidian_session", TouchInterval: time.Hour},
		SecretKey: []byte("an-instance-secret-for-the-auth-tests"),
	}
	// A second process over the same database: its own in-memory gate, nothing
	// shared with the first but the database.
	second := NewService(f.db, f.users, f.groups, f.settings, mail.New(mail.Config{}), cfg)

	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "one", Password: "a-good-password"}); err != nil {
		t.Fatalf("first process, second sign-up: %v", err)
	}
	if _, _, err := second.Register(ctx, RegisterInput{Username: "two", Password: "a-good-password"}); err != nil {
		t.Fatalf("second process, third sign-up: %v", err)
	}
	var throttled *SignupThrottleError
	if _, _, err := second.Register(ctx, RegisterInput{Username: "three", Password: "a-good-password"}); !errors.As(err, &throttled) {
		t.Fatalf("fourth sign-up in the minute on the second process = %v, want SignupThrottleError", err)
	}
}
