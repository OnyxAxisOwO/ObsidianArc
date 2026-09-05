package auth

import (
	"context"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Verification is what turns the domain allowlist from a speed bump into a
// gate. These cover the parts that decide whether it is a gate at all: that
// it is inert without a way to send mail, that the first account is never
// held back by it, and that a token behaves like a credential.

func TestVerificationIsInertWithoutMail(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// The setting is on, but nothing can send. An operator who does this
	// without SMTP would otherwise lock out every account created afterwards,
	// with no way for anyone to receive the link.
	if err := f.settings.Set(ctx, settings.VerifyEmail, "true"); err != nil {
		t.Fatal(err)
	}
	if f.auth.VerificationRequired() {
		t.Fatal("verification is required although no mail can be sent")
	}

	// And a registration made under it is usable immediately.
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Password: "a-good-password",
	}); err != nil {
		t.Fatalf("register founder: %v", err)
	}
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "someone", Email: "someone@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !account.EmailVerified {
		t.Error("account was held back although verification cannot work here")
	}
}

func TestExistingAccountsAreVerified(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "founder@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// The migration grandfathers everyone in, and an account created with no
	// requirement in force is verified for the same reason: there is nothing
	// outstanding to confirm.
	if !account.EmailVerified {
		t.Error("a new account is unverified with the requirement off")
	}
}

func TestAnAccountWithNoAddressIsNeverHeldBack(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}

	// Email is optional unless the instance requires it. Somebody who gave no
	// address has nothing to confirm, so holding them back would be a lock
	// with no key.
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "anonymous", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !account.EmailVerified {
		t.Error("an account with no address was marked unverified")
	}
}

func TestVerifyConsumesTheTokenExactlyOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "founder@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.auth.issueVerification(ctx, f.db, account.ID, account.Email)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := f.auth.Verify(ctx, token); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// A verification link is a credential. Replaying it must fail, or a
	// forwarded mail keeps working forever.
	if _, err := f.auth.Verify(ctx, token); err != ErrVerificationInvalid {
		t.Fatalf("replay err = %v, want ErrVerificationInvalid", err)
	}
}

// The token must not be recoverable from the database, the same way a session
// token is not.
func TestVerificationTokenIsStoredHashed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "founder@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.auth.issueVerification(ctx, f.db, account.ID, account.Email)
	if err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := f.db.QueryRow(ctx,
		`SELECT id FROM email_verifications WHERE user_id = ?`, account.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token {
		t.Fatal("the token itself is in the database")
	}
	if stored != digest(token) {
		t.Fatal("the stored value is not the token's digest")
	}
}

func TestExpiredTokenIsRefusedAndCleared(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "founder@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.auth.issueVerification(ctx, f.db, account.ID, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx,
		`UPDATE email_verifications SET expires_at = ? WHERE user_id = ?`,
		time.Now().Add(-time.Hour).UnixMilli(), account.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := f.auth.Verify(ctx, token); err != ErrVerificationExpired {
		t.Fatalf("err = %v, want ErrVerificationExpired", err)
	}

	var remaining int
	if err := f.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM email_verifications WHERE user_id = ?`, account.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Error("an expired token was left in place to be retried forever")
	}
}

// Issuing a new link has to invalidate the last one, or every link ever sent
// stays live for its full day.
func TestIssuingSupersedesTheOutstandingToken(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "founder@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := f.auth.issueVerification(ctx, f.db, account.ID, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.auth.issueVerification(ctx, f.db, account.ID, account.Email)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.auth.Verify(ctx, first); err != ErrVerificationInvalid {
		t.Fatalf("superseded token err = %v, want ErrVerificationInvalid", err)
	}
	if _, err := f.auth.Verify(ctx, second); err != nil {
		t.Fatalf("current token: %v", err)
	}
}

// The link verifies the address it was sent to, not whatever the account says
// by the time it is opened.
func TestVerifyConfirmsTheAddressTheLinkWasSentTo(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

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

	// Changed after the link went out.
	newAddress := "second@example.com"
	if _, err := f.users.UpdateProfile(ctx, nil, account.ID, user.ProfileUpdate{
		Email: &newAddress,
	}); err != nil {
		t.Fatalf("change address: %v", err)
	}

	if _, err := f.auth.Verify(ctx, token); err != nil {
		t.Fatalf("verify: %v", err)
	}

	after, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Email != "first@example.com" {
		t.Errorf("address = %q, want the one the link confirmed", after.Email)
	}
	if !after.EmailVerified {
		t.Error("account is still unverified")
	}
}

func TestEmptyTokenIsRefused(t *testing.T) {
	f := newFixture(t)
	for _, token := range []string{"", "   ", "not-a-real-token"} {
		if _, err := f.auth.Verify(context.Background(), token); err != ErrVerificationInvalid {
			t.Errorf("Verify(%q) err = %v, want ErrVerificationInvalid", token, err)
		}
	}
}

func TestPruneDropsOnlyExpiredVerifications(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "founder@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.issueVerification(ctx, f.db, account.ID, account.Email); err != nil {
		t.Fatal(err)
	}

	removed, err := f.auth.PruneVerifications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("pruned %d live tokens", removed)
	}

	if _, err := f.db.Exec(ctx, `UPDATE email_verifications SET expires_at = ?`,
		time.Now().Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	removed, err = f.auth.PruneVerifications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("pruned %d, want the one expired token", removed)
	}
}

// The gate itself: with somewhere to send mail and the setting on, a new
// account is held back and has a link waiting for it. The SMTP host does not
// have to answer — Register mails on a detached goroutine and logs a failure,
// precisely so an unreachable mail server cannot fail a registration that has
// already been written.
func TestVerificationHoldsBackNewAccountsWhenConfigured(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.auth.mailer = mail.New(mail.Config{
		Host:      "127.0.0.1",
		Port:      1,
		From:      "arc@example.com",
		PublicURL: "https://arc.example.com",
	})
	if err := f.settings.Set(ctx, settings.VerifyEmail, "true"); err != nil {
		t.Fatal(err)
	}
	if !f.auth.VerificationRequired() {
		t.Fatal("verification is not required although it is configured and switched on")
	}

	// The first account is the one that makes the instance administrable.
	founder, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "founder@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register founder: %v", err)
	}
	if !founder.EmailVerified {
		t.Error("the first account was held back")
	}

	later, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "later", Email: "later@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if later.EmailVerified {
		t.Fatal("an account created under the requirement is already verified")
	}

	var pending int
	if err := f.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM email_verifications WHERE user_id = ?`, later.ID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Errorf("%d outstanding links, want exactly one", pending)
	}
}

// Resend is a way to make this server mail a stranger, so it is throttled.
func TestResendIsThrottled(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.auth.mailer = mail.New(mail.Config{
		Host:      "127.0.0.1",
		Port:      1,
		From:      "arc@example.com",
		PublicURL: "https://arc.example.com",
	})
	if err := f.settings.Set(ctx, settings.VerifyEmail, "true"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "later", Email: "later@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Registration has just issued one, so an immediate resend is refused
	// before any mail is attempted.
	if err := f.auth.Resend(ctx, "Arc", account.ID); err != ErrResendTooSoon {
		t.Fatalf("err = %v, want ErrResendTooSoon", err)
	}
}

func TestResendRefusesAnAlreadyVerifiedAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "founder@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.auth.Resend(ctx, "Arc", account.ID); err != ErrAlreadyVerified {
		t.Fatalf("err = %v, want ErrAlreadyVerified", err)
	}
}
