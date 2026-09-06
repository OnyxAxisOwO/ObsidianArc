package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The profile form is the second way into the users table, and for a while it
// was the unguarded one: it reached the store directly, so an address typed
// here passed neither the domain allowlist nor the confirmation that the same
// address typed into the sign-up form has to pass.

func verifying(t *testing.T, f *fixture) {
	t.Helper()
	f.auth.mailer = mail.New(mail.Config{
		Host: "127.0.0.1", Port: 1,
		From: "arc@example.com", PublicURL: "https://arc.example.com",
	})
	if err := f.settings.Set(context.Background(), settings.VerifyEmail, "true"); err != nil {
		t.Fatal(err)
	}
	if !f.auth.VerificationRequired() {
		t.Fatal("verification is not required although it is configured and switched on")
	}
}

func ptr(value string) *string { return &value }

func TestAnAddressChangedInTheProfileIsHeldToTheAllowlist(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.settings.Set(ctx, settings.EmailDomains, "example.com"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "member", Email: "member@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err = f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{
		Email: ptr("member@elsewhere.test"),
	})
	var domain *EmailDomainError
	if !errors.As(err, &domain) {
		t.Fatalf("err = %v, want the domain refusal", err)
	}

	after, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Email != "member@example.com" {
		t.Errorf("address = %q, want the refused change not to have landed", after.Email)
	}
}

// An operator who narrows the allowlist after accounts exist has asked that
// nobody move to an address outside it, not that everybody already outside it
// is locked out of their own nickname.
func TestAnAddressThatDidNotMoveIsNotRecheckedAgainstTheAllowlist(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "founder@old.test", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.settings.Set(ctx, settings.EmailDomains, "example.com"); err != nil {
		t.Fatal(err)
	}

	updated, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{
		Nickname: ptr("Founder"), Email: ptr("founder@old.test"),
	})
	if err != nil {
		t.Fatalf("resubmitting an unchanged address: %v", err)
	}
	if updated.Nickname != "Founder" {
		t.Errorf("nickname = %q, want the change to have landed", updated.Nickname)
	}
}

func TestChangingAnAddressWithdrawsItsConfirmation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	verifying(t, f)

	// The first account is never held back, so it starts out confirmed —
	// which is exactly the state this used to carry over to any address its
	// owner cared to type.
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "founder@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !account.EmailVerified {
		t.Fatal("the first account was held back")
	}

	updated, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{
		Email: ptr("someone-else@example.com"),
	})
	if err != nil {
		t.Fatalf("change address: %v", err)
	}
	if updated.EmailVerified {
		t.Error("the account is still confirmed on an address nobody has proved they can read")
	}

	stored, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.EmailVerified {
		t.Error("the stored row is still confirmed")
	}

	var pending, address = 0, ""
	if err := f.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM email_verifications WHERE user_id = ?`, account.ID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("%d outstanding links, want exactly one for the new address", pending)
	}
	if err := f.db.QueryRow(ctx,
		`SELECT email FROM email_verifications WHERE user_id = ?`, account.ID).Scan(&address); err != nil {
		t.Fatal(err)
	}
	if address != "someone-else@example.com" {
		t.Errorf("link was issued for %q, want the new address", address)
	}
}

// The sequence that used to leave email and email_lower disagreeing: ask for
// a link, move on, then open the old one. It cannot happen now — the change
// drops what is outstanding — and this is the test that says so.
func TestALinkDoesNotSurviveTheAddressItWasIssuedFor(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	verifying(t, f)

	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "first@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.auth.issueVerification(ctx, f.db, account.ID, "first@example.com")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{
		Email: ptr("second@example.com"),
	}); err != nil {
		t.Fatalf("change address: %v", err)
	}

	if _, err := f.auth.Verify(ctx, token); !errors.Is(err, ErrVerificationInvalid) {
		t.Fatalf("stale link err = %v, want ErrVerificationInvalid", err)
	}
	after, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Email != "second@example.com" {
		t.Errorf("address = %q, want the one the account moved to", after.Email)
	}
}

// The display spelling and the folded one are the same fact stored twice, and
// the folded one is what login and the uniqueness index read. Registering with
// capitals and then confirming has to leave an account that can sign in.
func TestConfirmingLeavesTheFoldedAddressUsable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	verifying(t, f)

	// Not the first account: that one is never held back, so it would have
	// nothing to confirm.
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "first", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "Founder.Mixed@Example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.EmailVerified {
		t.Fatal("the account was confirmed without opening anything")
	}

	var token string
	if err := f.db.QueryRow(ctx,
		`SELECT id FROM email_verifications WHERE user_id = ?`, account.ID).Scan(&token); err != nil {
		t.Fatalf("registration issued no link: %v", err)
	}
	// The row keeps the digest, so the link itself has to be reissued to be
	// opened here.
	token, err = f.auth.issueVerification(ctx, f.db, account.ID, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.Verify(ctx, token); err != nil {
		t.Fatalf("verify: %v", err)
	}

	found, _, err := f.users.CredentialsByLogin(ctx, "founder.mixed@example.com")
	if err != nil {
		t.Fatalf("sign in with the folded address: %v", err)
	}
	if found.ID != account.ID {
		t.Errorf("resolved %q, want the account that confirmed the link", found.ID)
	}
	if !found.EmailVerified {
		t.Error("the account is still unconfirmed after opening its own link")
	}
}

func TestClearingAQQNumberIsHeldToTheRegistrationRequirement(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", QQ: "1234567", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.settings.Set(ctx, settings.QQRequirement, settings.QQRequired); err != nil {
		t.Fatal(err)
	}

	if _, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{
		QQ: ptr(""),
	}); !errors.Is(err, user.ErrQQRequired) {
		t.Fatalf("err = %v, want user.ErrQQRequired", err)
	}
}
