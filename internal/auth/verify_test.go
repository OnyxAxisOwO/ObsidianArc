package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
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

func TestVerificationCodeConfirmsAndConsumesTheLink(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	verifying(t, f)
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "member", Email: "member@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.auth.issueVerification(ctx, f.db, account.ID, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	code := f.auth.verificationCode(token)

	if err := f.auth.VerifyCode(ctx, account.ID, code); err != nil {
		t.Fatalf("verify code: %v", err)
	}
	if _, err := f.auth.Verify(ctx, token); !errors.Is(err, ErrVerificationInvalid) {
		t.Fatalf("link remained usable after code verification: %v", err)
	}
	if err := f.auth.VerifyCode(ctx, account.ID, code); !errors.Is(err, ErrVerificationInvalid) {
		t.Fatalf("code replay err = %v, want ErrVerificationInvalid", err)
	}
}

func TestVerificationLinkConsumesTheCode(t *testing.T) {
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
	code := f.auth.verificationCode(token)
	if _, err := f.auth.Verify(ctx, token); err != nil {
		t.Fatalf("verify link: %v", err)
	}
	if err := f.auth.VerifyCode(ctx, account.ID, code); !errors.Is(err, ErrVerificationInvalid) {
		t.Fatalf("code remained usable after link verification: %v", err)
	}
}

func TestVerificationCodeExpiresWithoutExpiringTheLink(t *testing.T) {
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
	if _, err := f.db.Exec(ctx, `UPDATE email_verifications SET code_expires_at = ? WHERE user_id = ?`,
		time.Now().Add(-time.Second).UnixMilli(), account.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.auth.VerifyCode(ctx, account.ID, f.auth.verificationCode(token)); !errors.Is(err, ErrVerificationCodeExpired) {
		t.Fatalf("expired code err = %v, want ErrVerificationCodeExpired", err)
	}
	if _, err := f.auth.Verify(ctx, token); err != nil {
		t.Fatalf("the still-live link stopped working after its code expired: %v", err)
	}
}

func TestFiveWrongCodesLockCodesButLeaveTheLinkUsable(t *testing.T) {
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
	correct := f.auth.verificationCode(token)
	wrong := "000000"
	if wrong == correct {
		wrong = "000001"
	}
	for i := 0; i < verificationMaxTries; i++ {
		want := ErrVerificationInvalid
		if i == verificationMaxTries-1 {
			want = ErrVerificationCodeLimited
		}
		if err := f.auth.VerifyCode(ctx, account.ID, wrong); !errors.Is(err, want) {
			t.Fatalf("wrong attempt %d err = %v, want %v", i+1, err, want)
		}
	}
	if err := f.auth.VerifyCode(ctx, account.ID, correct); !errors.Is(err, ErrVerificationCodeLimited) {
		t.Fatalf("the locked account returned %v, want ErrVerificationCodeLimited", err)
	}
	var attempts int
	if err := f.db.QueryRow(ctx, `SELECT code_attempts FROM email_verifications WHERE user_id = ?`, account.ID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != verificationMaxTries {
		t.Fatalf("wrong attempts = %d, want the locked code to stop at %d", attempts, verificationMaxTries)
	}
	var accountAttempts int
	if err := f.db.QueryRow(ctx,
		`SELECT attempts FROM email_verification_attempts WHERE user_id = ?`, account.ID).Scan(&accountAttempts); err != nil {
		t.Fatal(err)
	}
	if accountAttempts != verificationMaxTries {
		t.Fatalf("account wrong attempts = %d, want %d", accountAttempts, verificationMaxTries)
	}
	if _, err := f.auth.Verify(ctx, token); err != nil {
		t.Fatalf("attempt limit invalidated the link too: %v", err)
	}
	if err := f.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM email_verification_attempts WHERE user_id = ?`, account.ID).Scan(&accountAttempts); err != nil {
		t.Fatal(err)
	}
	if accountAttempts != 0 {
		t.Fatalf("successful link left an attempt budget row: %d", accountAttempts)
	}
}

func TestVerificationCodeBudgetSurvivesResendAndAddressChange(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	verifying(t, f)
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "member", Email: "member@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	oldToken, err := f.auth.issueVerification(ctx, f.db, account.ID, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	wrong := wrongVerificationCode(f.auth, oldToken)
	for i := 0; i < 3; i++ {
		if err := f.auth.VerifyCode(ctx, account.ID, wrong); !errors.Is(err, ErrVerificationInvalid) {
			t.Fatalf("wrong code before address change %d err = %v", i+1, err)
		}
	}

	newAddress := "member2@example.com"
	updated, err := f.auth.UpdateProfile(ctx, account.ID, user.ProfileUpdate{Email: &newAddress})
	if err != nil {
		t.Fatalf("change address: %v", err)
	}
	if updated.Email != newAddress || updated.EmailVerified {
		t.Fatalf("changed account = email %q, verified %t; want new unverified address", updated.Email, updated.EmailVerified)
	}
	var accountAttempts int
	if err := f.db.QueryRow(ctx,
		`SELECT attempts FROM email_verification_attempts WHERE user_id = ?`, account.ID).Scan(&accountAttempts); err != nil {
		t.Fatal(err)
	}
	if accountAttempts != 3 {
		t.Fatalf("address change reset the account budget to %d attempts, want 3", accountAttempts)
	}

	newToken, err := f.auth.issueVerification(ctx, f.db, account.ID, newAddress)
	if err != nil {
		t.Fatal(err)
	}
	// Resend inserts its new credential before SMTP, then retires the old row
	// only after delivery. Model those two database writes without a network
	// dependency; the account-level budget is intentionally outside both rows.
	resentToken, err := randomVerificationToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.auth.insertVerification(ctx, f.db, account.ID, newAddress, resentToken, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx,
		`DELETE FROM email_verifications WHERE user_id = ? AND id <> ?`, account.ID, digest(resentToken)); err != nil {
		t.Fatal(err)
	}
	wrong = wrongVerificationCode(f.auth, oldToken, newToken, resentToken)
	for i := 0; i < 2; i++ {
		want := ErrVerificationInvalid
		if i == 1 {
			want = ErrVerificationCodeLimited
		}
		if err := f.auth.VerifyCode(ctx, account.ID, wrong); !errors.Is(err, want) {
			t.Fatalf("wrong code after resend %d err = %v, want %v", i+1, err, want)
		}
	}
	if err := f.auth.VerifyCode(ctx, account.ID, f.auth.verificationCode(resentToken)); !errors.Is(err, ErrVerificationCodeLimited) {
		t.Fatalf("resent correct code worked after five account guesses: %v", err)
	}
	if _, err := f.auth.Verify(ctx, resentToken); err != nil {
		t.Fatalf("the link stopped working after the code budget was spent: %v", err)
	}
}

func wrongVerificationCode(s *Service, tokens ...string) string {
	correct := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		correct[s.verificationCode(token)] = struct{}{}
	}
	for value := 0; value < 1_000_000; value++ {
		candidate := fmt.Sprintf("%06d", value)
		if _, found := correct[candidate]; !found {
			return candidate
		}
	}
	panic("all six-digit codes were marked correct")
}

func TestVerificationCodeBudgetExpiresAfter24Hours(t *testing.T) {
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
	wrong := wrongVerificationCode(f.auth, token)
	for i := 0; i < verificationMaxTries; i++ {
		want := ErrVerificationInvalid
		if i == verificationMaxTries-1 {
			want = ErrVerificationCodeLimited
		}
		if err := f.auth.VerifyCode(ctx, account.ID, wrong); !errors.Is(err, want) {
			t.Fatalf("wrong attempt %d err = %v, want %v", i+1, err, want)
		}
	}
	if _, err := f.db.Exec(ctx,
		`UPDATE email_verification_attempts SET last_attempt_at = ? WHERE user_id = ?`,
		time.Now().Add(-verificationTryWindow-time.Millisecond).UnixMilli(), account.ID); err != nil {
		t.Fatal(err)
	}
	newToken, err := f.auth.issueVerification(ctx, f.db, account.ID, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.auth.VerifyCode(ctx, account.ID, f.auth.verificationCode(newToken)); err != nil {
		t.Fatalf("correct code remained locked after the 24-hour window: %v", err)
	}
}

func TestConcurrentWrongCodesCannotExceedFiveAttempts(t *testing.T) {
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
	wrong := "123456"
	if wrong == f.auth.verificationCode(token) {
		wrong = "123457"
	}
	const callers = 12
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < callers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if err := f.auth.VerifyCode(ctx, account.ID, wrong); !errors.Is(err, ErrVerificationInvalid) && !errors.Is(err, ErrVerificationCodeLimited) {
				t.Errorf("wrong code err = %v, want ErrVerificationInvalid or ErrVerificationCodeLimited", err)
			}
		}()
	}
	close(start)
	workers.Wait()
	var attempts int
	if err := f.db.QueryRow(ctx, `SELECT code_attempts FROM email_verifications WHERE user_id = ?`, account.ID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != verificationMaxTries {
		t.Fatalf("concurrent attempts = %d, want exactly %d", attempts, verificationMaxTries)
	}
	if err := f.db.QueryRow(ctx,
		`SELECT attempts FROM email_verification_attempts WHERE user_id = ?`, account.ID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != verificationMaxTries {
		t.Fatalf("concurrent account attempts = %d, want exactly %d", attempts, verificationMaxTries)
	}
}

func TestVerificationCodeLimitHasSpecificHTTPError(t *testing.T) {
	var response *httpx.Error
	if err := verificationError(ErrVerificationCodeLimited); !errors.As(err, &response) {
		t.Fatalf("error mapping returned %T, want *httpx.Error", err)
	}
	if response.Status != http.StatusTooManyRequests || response.Code != "verification_code_limited" {
		t.Fatalf("HTTP mapping = status %d code %q, want 429 verification_code_limited", response.Status, response.Code)
	}
}

func TestConcurrentLinkAndCodeVerificationConsumeOneCredentialSet(t *testing.T) {
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
	code := f.auth.verificationCode(token)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; _, err := f.auth.Verify(ctx, token); results <- err }()
	go func() { <-start; results <- f.auth.VerifyCode(ctx, account.ID, code) }()
	close(start)
	first, second := <-results, <-results
	successes := 0
	invalid := 0
	for _, err := range []error{first, second} {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrVerificationInvalid) {
			invalid++
		} else {
			t.Fatalf("verification returned unexpected error: %v", err)
		}
	}
	if successes != 1 || invalid != 1 {
		t.Fatalf("link/code results = %v, %v; want one success and one invalid", first, second)
	}
}

func TestCodeVerificationDuringResendCountsBothRowsAndConsumesBoth(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	verifying(t, f)
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "member", Email: "member@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	oldToken, err := f.auth.issueVerification(ctx, f.db, account.ID, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `UPDATE email_verifications SET created_at = ? WHERE id = ?`,
		time.Now().Add(-3*maxOutstandingResend).UnixMilli(), digest(oldToken)); err != nil {
		t.Fatal(err)
	}
	oldCode := f.auth.verificationCode(oldToken)
	reachedMail, releaseMail := holdVerificationMail(t, f)
	resendResult := make(chan error, 1)
	go func() { resendResult <- f.auth.Resend(ctx, "Arc", account.ID) }()
	select {
	case <-reachedMail:
	case <-time.After(5 * time.Second):
		t.Fatal("resend did not reach the held mail connection")
	}

	rows, err := f.db.Query(ctx,
		`SELECT id, code_digest, code_attempts FROM email_verifications WHERE user_id = ?`, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	type storedCode struct {
		id, digest string
		attempts   int
	}
	var stored []storedCode
	for rows.Next() {
		var item storedCode
		if err := rows.Scan(&item.id, &item.digest, &item.attempts); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		stored = append(stored, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(stored) != 2 {
		t.Fatalf("verification rows while resend was in SMTP = %d, want old and new", len(stored))
	}
	var newCodeDigest string
	for _, item := range stored {
		if item.id == digest(oldToken) {
			if item.digest != f.auth.codeDigest(oldCode) {
				t.Fatal("old code changed during resend")
			}
		} else {
			newCodeDigest = item.digest
		}
	}
	wrong := "000000"
	for value := 0; f.auth.codeDigest(wrong) == f.auth.codeDigest(oldCode) || f.auth.codeDigest(wrong) == newCodeDigest; value++ {
		wrong = fmt.Sprintf("%06d", (value+1)%1_000_000)
	}
	if err := f.auth.VerifyCode(ctx, account.ID, wrong); !errors.Is(err, ErrVerificationInvalid) {
		t.Fatalf("wrong code during resend err = %v, want ErrVerificationInvalid", err)
	}
	rows, err = f.db.Query(ctx, `SELECT code_attempts FROM email_verifications WHERE user_id = ?`, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	for rows.Next() {
		var count int
		if err := rows.Scan(&count); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("active code attempt count = %d, want both rows to count the guess once", count)
		}
		attempts++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if attempts != 2 {
		t.Fatalf("wrong guess updated %d code rows, want both", attempts)
	}
	if err := f.auth.VerifyCode(ctx, account.ID, oldCode); err != nil {
		t.Fatalf("the old code stopped working while the new email was in flight: %v", err)
	}
	var remaining int
	if err := f.db.QueryRow(ctx, `SELECT COUNT(*) FROM email_verifications WHERE user_id = ?`, account.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("successful old code left %d link/code rows behind", remaining)
	}
	releaseMail()
	if err := <-resendResult; err == nil {
		t.Fatal("held TLS connection unexpectedly delivered mail")
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

// A link sent to one address must never confirm another. That is the property
// this has always protected; what changed is how.
//
// It used to be held by writing the link's address back onto the account, so
// opening a stale link moved the owner somewhere they had left. Now the link
// simply does not match: confirming an address the account no longer has is
// not something to do quietly, and reverting them to it is worse.
func TestAStaleLinkConfirmsNothingAndMovesNobody(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	verifying(t, f)

	// Not the first account: that one is never held back, so it would arrive
	// already confirmed and the assertions below would pass for the wrong
	// reason.
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "first", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "first@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.EmailVerified {
		t.Fatal("the account arrived confirmed; this test would prove nothing")
	}
	token, err := f.auth.issueVerification(ctx, f.db, account.ID, "first@example.com")
	if err != nil {
		t.Fatal(err)
	}

	// Moved by a path that does not withdraw the link — which is what the
	// administrator's form still does.
	moved := "second@example.com"
	if _, err := f.users.UpdateProfile(ctx, nil, account.ID, user.ProfileUpdate{Email: &moved}); err != nil {
		t.Fatalf("change address: %v", err)
	}

	if _, err := f.auth.Verify(ctx, token); !errors.Is(err, ErrVerificationInvalid) {
		t.Fatalf("verify returned %v, want ErrVerificationInvalid", err)
	}

	after, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Email != moved {
		t.Errorf("address = %q; a stale link moved the account off %q", after.Email, moved)
	}
	if after.EmailVerified {
		t.Error("an address nobody confirmed is marked confirmed")
	}

	// Spent either way, so it cannot be opened again after the next move.
	var outstanding int
	if err := f.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM email_verifications WHERE user_id = ?`, account.ID).Scan(&outstanding); err != nil {
		t.Fatal(err)
	}
	if outstanding != 0 {
		t.Errorf("%d links still outstanding", outstanding)
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

func TestVerificationRequiresEmailForLaterAccountsButExemptsFirstAdmin(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	verifying(t, f)
	founder, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register first admin without email: %v", err)
	}
	if !founder.EmailVerified {
		t.Fatal("first admin was held back despite having no email to confirm")
	}
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "later", Password: "a-good-password",
	}); !errors.Is(err, ErrEmailRequired) {
		t.Fatalf("later account without email err = %v, want ErrEmailRequired", err)
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

func TestFailedResendKeepsTheOldLinkAndAllowsImmediateRetry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	verifying(t, f)
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "member", Email: "member@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	oldToken, err := f.auth.issueVerification(ctx, f.db, account.ID, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	oldCreated := time.Now().Add(-3 * maxOutstandingResend).UnixMilli()
	if _, err := f.db.Exec(ctx, `UPDATE email_verifications SET created_at = ? WHERE user_id = ?`, oldCreated, account.ID); err != nil {
		t.Fatal(err)
	}
	var oldID, oldCodeDigest string
	if err := f.db.QueryRow(ctx,
		`SELECT id, code_digest FROM email_verifications WHERE user_id = ?`, account.ID).Scan(&oldID, &oldCodeDigest); err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		err := f.auth.Resend(ctx, "Arc", account.ID)
		if err == nil || errors.Is(err, ErrResendTooSoon) {
			t.Fatalf("failed resend %d err = %v; want SMTP failure and an immediate retry window", attempt, err)
		}
		var remaining int
		var id, codeDigest string
		if err := f.db.QueryRow(ctx,
			`SELECT COUNT(*), MIN(id), MIN(code_digest) FROM email_verifications WHERE user_id = ?`, account.ID).
			Scan(&remaining, &id, &codeDigest); err != nil {
			t.Fatal(err)
		}
		if remaining != 1 || id != oldID || codeDigest != oldCodeDigest {
			t.Fatalf("failed resend changed old credential: rows=%d id=%q code digest matches=%t", remaining, id, codeDigest == oldCodeDigest)
		}
	}
	if _, err := f.auth.Verify(ctx, oldToken); err != nil {
		t.Fatalf("old link stopped working after delivery failed: %v", err)
	}
}

// Resend is a way to make this server mail a stranger, so it is throttled —
// but only against a client that asks politely. The timestamp was read and
// then written with nothing holding the two together, so eight requests in
// parallel all found no outstanding link, all issued one and all sent. The
// limit has to be one transaction over the owner's row, or it is a limit on
// sequential requests.
func TestParallelResendsLeaveWithOneLink(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	verifying(t, f)
	if err := f.settings.Set(ctx, settings.VerifyEmail, "true"); err != nil {
		t.Fatal(err)
	}
	// The first account is exempt from verification, so it goes first and
	// "later" is the account this is about.
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
	// Registration's own link would throttle the first resend, and this is
	// about the window being open rather than about that one being closed.
	if _, err := f.db.Exec(ctx,
		`DELETE FROM email_verifications WHERE user_id = ?`, account.ID); err != nil {
		t.Fatal(err)
	}
	reachedMail, releaseMail := holdVerificationMail(t, f)

	const attempts = 8
	var (
		wg      sync.WaitGroup
		results = make(chan error, attempts)
	)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- f.auth.Resend(ctx, "Arc", account.ID)
		}()
	}
	select {
	case <-reachedMail:
	case <-time.After(5 * time.Second):
		t.Fatal("no resend reached the held mail connection")
	}

	refused := 0
	for i := 0; i < attempts-1; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, ErrResendTooSoon) {
				t.Fatalf("concurrent resend returned %v, want ErrResendTooSoon", err)
			}
			refused++
		case <-time.After(5 * time.Second):
			t.Fatal("contending resend did not finish while SMTP was held open")
		}
	}
	releaseMail()
	var deliveredErr error
	select {
	case deliveredErr = <-results:
	case <-time.After(5 * time.Second):
		t.Fatal("held mail request did not return after the connection closed")
	}
	wg.Wait()
	if deliveredErr == nil || errors.Is(deliveredErr, ErrResendTooSoon) {
		t.Fatalf("the single request reaching SMTP returned %v; want the blocked relay failure", deliveredErr)
	}
	if refused != attempts-1 {
		t.Fatalf("%d resends were refused as too soon, want %d", refused, attempts-1)
	}
}

func holdVerificationMail(t *testing.T, f *fixture) (<-chan struct{}, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.auth.mailer.Update(mail.Config{
		Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port,
		From: "arc@example.com", PublicURL: "https://arc.example.com", ImplicitTLS: true,
	})
	reached := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	releaseMail := func() { once.Do(func() { close(release) }) }
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		close(reached)
		<-release
		_ = conn.Close()
	}()
	t.Cleanup(func() {
		releaseMail()
		_ = listener.Close()
	})
	return reached, releaseMail
}
