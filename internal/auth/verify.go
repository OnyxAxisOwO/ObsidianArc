package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// Email verification.
//
// The domain allowlist beside it only checks the string someone typed, which
// stops nobody willing to type "someone@qq.com". This is what turns that from
// a speed bump into a gate: it costs an attacker a working mailbox per
// account, which is the cost that makes scripted sign-ups uneconomic.
//
// A verification token is handled the way a session token is: random bytes go
// to the user, a SHA-256 digest goes to the database. A leaked database hands
// out no working links.

const (
	verificationTTL      = 24 * time.Hour
	verificationBytes    = 32
	maxOutstandingResend = 2 * time.Minute
)

var (
	ErrVerificationInvalid = errors.New("auth: that verification link is not valid")
	ErrVerificationExpired = errors.New("auth: that verification link has expired")
	ErrAlreadyVerified     = errors.New("auth: that address is already verified")
	ErrNoAddress           = errors.New("auth: this account has no email address to verify")
	ErrResendTooSoon       = errors.New("auth: a link was just sent; check the address first")
)

// VerificationRequired reports whether unverified accounts should be held
// back. False whenever mail cannot be sent, whatever the setting says: an
// operator who switches this on without SMTP would otherwise lock out every
// account created afterwards, with no way for anyone to get the link.
func (s *Service) VerificationRequired() bool {
	return s.mailer != nil && s.mailer.Configured() && s.settings.Bool(settings.VerifyEmail)
}

// MailConfigured is what the admin screen reads to decide whether to offer
// the setting at all.
func (s *Service) MailConfigured() bool {
	return s.mailer != nil && s.mailer.Configured()
}

// issueVerification stores a fresh token and returns the one to put in a
// link. Any outstanding tokens for the account are dropped: a link that has
// been superseded should stop working.
func (s *Service) issueVerification(ctx context.Context, q database.Queryer, userID, email string) (string, error) {
	raw := make([]byte, verificationBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: verification token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	if _, err := q.Exec(ctx, `DELETE FROM email_verifications WHERE user_id = ?`, userID); err != nil {
		return "", fmt.Errorf("auth: clear verifications: %w", err)
	}
	now := time.Now()
	_, err := q.Exec(ctx,
		`INSERT INTO email_verifications (id, user_id, email, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		digest(token), userID, email, now.Add(verificationTTL).UnixMilli(), now.UnixMilli())
	if err != nil {
		return "", fmt.Errorf("auth: store verification: %w", err)
	}
	return token, nil
}

// SendVerification mails the link. Failures are the caller's to decide about:
// registration does not fail because a mail server was briefly unreachable,
// but a resend says so, because the user is standing there waiting for it.
func (s *Service) SendVerification(ctx context.Context, siteName, email, token string) error {
	if s.mailer == nil || !s.mailer.Configured() {
		return mail.ErrNotConfigured
	}
	base := s.mailer.PublicURL()
	if base == "" {
		return errors.New("auth: no public URL is configured for verification links")
	}
	link := base + "/verify?token=" + token

	return s.mailer.Send(ctx, mail.Message{
		To:      email,
		Subject: siteName + " — verify your email address",
		Body: strings.Join([]string{
			"Open this link to finish setting up your account:",
			"",
			link,
			"",
			"The link is good for 24 hours. If you did not create an account,",
			"you can ignore this message — nothing happens until it is opened.",
		}, "\n"),
	})
}

// Verify consumes a token. It confirms the address the link was issued for,
// and only while the account still has it.
//
// It used to write that address onto the row, which is a different thing and
// the source of three faults at once. A link opened while a profile change was
// committing put the old address back and marked it confirmed — the read of
// the token happens before the transaction, so the deletion that was supposed
// to withdraw the link came too late to stop it, and the change the owner had
// just made was silently undone. Where somebody else had since registered the
// old address, the write hit the uniqueness index on email_lower instead, and
// the error had no case in verificationError: a 500, with the token's deletion
// rolled back beside it, so the same link failed the same way for a full day.
//
// Confirming rather than assigning removes all of it. The address is already
// on the row — registration and the profile form both put it there — so there
// is nothing here to write but the flag, and a link for an address the account
// has moved off simply matches no row. That is also why this needs no lock:
// whichever way the two transactions interleave, the value the WHERE reads is
// the one the other would have changed.
func (s *Service) Verify(ctx context.Context, token string) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", ErrVerificationInvalid
	}

	var userID, email string
	var expiresAt int64
	err := s.db.QueryRow(ctx,
		`SELECT user_id, email, expires_at FROM email_verifications WHERE id = ?`, digest(token)).
		Scan(&userID, &email, &expiresAt)
	if err != nil {
		if database.IsNotFound(err) {
			return "", ErrVerificationInvalid
		}
		return "", fmt.Errorf("auth: read verification: %w", err)
	}

	if time.Now().UnixMilli() > expiresAt {
		// Cleared on sight, so an expired link cannot be retried forever.
		_, _ = s.db.Exec(ctx, `DELETE FROM email_verifications WHERE id = ?`, digest(token))
		return "", ErrVerificationExpired
	}

	confirmed := false
	err = s.db.Tx(ctx, func(tx *database.Tx) error {
		// email_lower rather than email: it is what the login query and the
		// uniqueness index work in, so it is the account's identity, and the
		// display spelling is the owner's to change without unconfirming
		// themselves.
		marked, err := tx.Exec(ctx,
			`UPDATE users SET email_verified = ?, updated_at = ?
			 WHERE id = ? AND email_lower = ?`,
			true, time.Now().UnixMilli(), userID, strings.ToLower(email))
		if err != nil {
			return fmt.Errorf("auth: mark verified: %w", err)
		}
		// No row means the account is no longer at the address this link was
		// issued for. The link is spent either way — leaving it usable is what
		// would let it be opened again after the next move.
		affected, err := marked.RowsAffected()
		if err != nil {
			// Whether the link matched is the whole answer here, and a driver
			// that cannot say must not be read as a yes.
			return fmt.Errorf("auth: mark verified: %w", err)
		}
		confirmed = affected > 0

		// Spent either way. Returning the refusal from inside here would roll
		// this back with it, and the link would still be open for the next
		// attempt — so the transaction commits and the caller is told after.
		scope, args := "user_id = ?", []any{userID}
		if !confirmed {
			scope, args = "id = ?", []any{digest(token)}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM email_verifications WHERE `+scope, args...); err != nil {
			return fmt.Errorf("auth: clear verifications: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if !confirmed {
		return "", ErrVerificationInvalid
	}
	return userID, nil
}

// Resend issues a new link for an account that has not verified yet.
func (s *Service) Resend(ctx context.Context, siteName string, userID string) error {
	account, err := s.users.ByID(ctx, nil, userID)
	if err != nil {
		return err
	}
	if account.EmailVerified {
		return ErrAlreadyVerified
	}
	if strings.TrimSpace(account.Email) == "" {
		return ErrNoAddress
	}

	// One link every couple of minutes. Without this the resend button is a
	// way to have this server mail a stranger repeatedly.
	var createdAt int64
	err = s.db.QueryRow(ctx,
		`SELECT created_at FROM email_verifications WHERE user_id = ?`, userID).Scan(&createdAt)
	if err == nil && time.Since(time.UnixMilli(createdAt)) < maxOutstandingResend {
		return ErrResendTooSoon
	}
	if err != nil && !database.IsNotFound(err) {
		return fmt.Errorf("auth: read verification: %w", err)
	}

	token, err := s.issueVerification(ctx, s.db, userID, account.Email)
	if err != nil {
		return err
	}
	return s.SendVerification(ctx, siteName, account.Email, token)
}

// PruneVerifications drops links nobody can use any more. Called by the same
// janitor that expires sessions.
func (s *Service) PruneVerifications(ctx context.Context) (int64, error) {
	result, err := s.db.Exec(ctx,
		`DELETE FROM email_verifications WHERE expires_at < ?`, time.Now().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("auth: prune verifications: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected, nil
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
