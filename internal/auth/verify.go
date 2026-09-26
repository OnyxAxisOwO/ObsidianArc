package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
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
	verificationTTL       = 24 * time.Hour
	verificationBytes     = 32
	verificationCodeTTL   = 10 * time.Minute
	verificationMaxTries  = 5
	verificationTryWindow = 24 * time.Hour
	maxOutstandingResend  = 2 * time.Minute
)

var (
	ErrVerificationInvalid     = errors.New("auth: that verification link is not valid")
	ErrVerificationExpired     = errors.New("auth: that verification link has expired")
	ErrVerificationCodeExpired = errors.New("auth: that verification code has expired")
	ErrVerificationCodeLimited = errors.New("auth: too many incorrect verification code attempts")
	ErrAlreadyVerified         = errors.New("auth: that address is already verified")
	ErrNoAddress               = errors.New("auth: this account has no email address to verify")
	ErrResendTooSoon           = errors.New("auth: a link was just sent; check the address first")
)

// VerificationRequired reports whether unverified accounts should be held
// back. False whenever mail cannot be sent, whatever the setting says: an
// operator who switches this on without SMTP would otherwise lock out every
// account created afterwards, with no way for anyone to get the link.
func (s *Service) VerificationRequired() bool {
	return len(s.verificationKey) > 0 && s.mailer != nil && s.mailer.VerificationReady() && s.settings.Bool(settings.VerifyEmail)
}

// MailConfigured is what the admin screen reads to decide whether to offer
// the setting at all.
func (s *Service) MailConfigured() bool {
	return len(s.verificationKey) > 0 && s.mailer != nil && s.mailer.VerificationReady()
}

func (s *Service) verificationCode(token string) string {
	mac := hmac.New(sha256.New, s.verificationKey)
	_, _ = mac.Write([]byte("email-verification-code:" + token))
	value := binary.BigEndian.Uint64(mac.Sum(nil)[:8]) % 1_000_000
	return fmt.Sprintf("%06d", value)
}

func (s *Service) codeDigest(code string) string {
	mac := hmac.New(sha256.New, s.verificationKey)
	_, _ = mac.Write([]byte("email-verification-claim:" + code))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// issueVerification stores a fresh token and returns the one to put in a
// link. Any outstanding tokens for the account are dropped: a link that has
// been superseded should stop working.
func (s *Service) issueVerification(ctx context.Context, q database.Queryer, userID, email string) (string, error) {
	token, err := randomVerificationToken()
	if err != nil {
		return "", err
	}
	if err := s.replaceVerification(ctx, q, userID, email, token, time.Now()); err != nil {
		return "", err
	}
	return token, nil
}

// replaceVerification keeps profile changes and initial issuance to one
// current credential. Resend inserts alongside the old credential until mail
// succeeds, so a failed provider call cannot strand the account.
func (s *Service) replaceVerification(ctx context.Context, q database.Queryer, userID, email, token string, issuedAt time.Time) error {
	if _, err := q.Exec(ctx, `DELETE FROM email_verifications WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("auth: clear verifications: %w", err)
	}
	return s.insertVerification(ctx, q, userID, email, token, issuedAt)
}

func (s *Service) insertVerification(ctx context.Context, q database.Queryer, userID, email, token string, issuedAt time.Time) error {
	_, err := q.Exec(ctx,
		`INSERT INTO email_verifications (id, user_id, email, expires_at, created_at, code_digest, code_expires_at, code_attempts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0)`,
		digest(token), userID, email, issuedAt.Add(verificationTTL).UnixMilli(), issuedAt.UnixMilli(),
		s.codeDigest(s.verificationCode(token)), issuedAt.Add(verificationCodeTTL).UnixMilli())
	if err != nil {
		return fmt.Errorf("auth: store verification: %w", err)
	}
	return nil
}

// SendVerification mails the link. Failures are the caller's to decide about:
// registration does not fail because a mail server was briefly unreachable,
// but a resend says so, because the user is standing there waiting for it.
func (s *Service) SendVerification(ctx context.Context, siteName, email, token string) error {
	if len(s.verificationKey) == 0 || s.mailer == nil || !s.mailer.VerificationReady() {
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
			"Open this link, then confirm on the page:",
			"",
			link,
			"",
			"Or enter this 6-digit verification code while signed in: " + s.verificationCode(token),
			"",
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

	var userID string
	err := s.db.QueryRow(ctx,
		`SELECT user_id FROM email_verifications WHERE id = ?`, digest(token)).Scan(&userID)
	if err != nil {
		if database.IsNotFound(err) {
			return "", ErrVerificationInvalid
		}
		return "", fmt.Errorf("auth: read verification: %w", err)
	}

	confirmed := false
	var outcome error
	err = s.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
			return fmt.Errorf("auth: lock verification owner: %w", err)
		}
		var email string
		var expiresAt int64
		if err := tx.QueryRow(ctx,
			`SELECT email, expires_at FROM email_verifications WHERE id = ? AND user_id = ?`, digest(token), userID).
			Scan(&email, &expiresAt); err != nil {
			if database.IsNotFound(err) {
				outcome = ErrVerificationInvalid
				return nil
			}
			return fmt.Errorf("auth: reread verification: %w", err)
		}
		if time.Now().UnixMilli() > expiresAt {
			if _, err := tx.Exec(ctx, `DELETE FROM email_verifications WHERE user_id = ? AND id = ?`, userID, digest(token)); err != nil {
				return err
			}
			outcome = ErrVerificationExpired
			return nil
		}
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
		if confirmed {
			if _, err := tx.Exec(ctx, `DELETE FROM email_verification_attempts WHERE user_id = ?`, userID); err != nil {
				return fmt.Errorf("auth: clear verification attempts: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if outcome != nil {
		return "", outcome
	}
	if !confirmed {
		return "", ErrVerificationInvalid
	}
	// The other moment invite.Store.Reward is asked about an account: one
	// registered unverified through a personal code qualified for nothing
	// at Register's own call, and this is the only other event this package
	// tells that package about it again.
	if s.RewardInvite != nil {
		s.RewardInvite(ctx, userID, s.VerificationRequired())
	}
	return userID, nil
}

// VerifyCode serializes guesses and successful consumption through the owner
// row so the account-wide budget survives credential replacement across instances.
func (s *Service) VerifyCode(ctx context.Context, userID, code string) error {
	if len(code) != 6 {
		return ErrVerificationInvalid
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return ErrVerificationInvalid
		}
	}
	var outcome error
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
			return fmt.Errorf("auth: lock verification owner: %w", err)
		}
		account, err := s.users.ByID(ctx, tx, userID)
		if err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		attempts, err := verificationAttemptBudget(ctx, tx, userID, now)
		if err != nil {
			return err
		}
		if attempts >= verificationMaxTries {
			outcome = ErrVerificationCodeLimited
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT id, email, code_digest, code_expires_at, code_attempts FROM email_verifications WHERE user_id = ?`, userID)
		if err != nil {
			return fmt.Errorf("auth: read verification codes: %w", err)
		}
		type candidate struct {
			id, email, stored string
			expires           int64
			attempts          int
		}
		var candidates []candidate
		for rows.Next() {
			var item candidate
			if err := rows.Scan(&item.id, &item.email, &item.stored, &item.expires, &item.attempts); err != nil {
				rows.Close()
				return fmt.Errorf("auth: scan verification code: %w", err)
			}
			candidates = append(candidates, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("auth: read verification codes: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("auth: close verification codes: %w", err)
		}

		provided, _ := base64.RawURLEncoding.DecodeString(s.codeDigest(code))
		matched := false
		active := 0
		codeExpired := false
		for _, item := range candidates {
			if strings.ToLower(item.email) != strings.ToLower(account.Email) || item.stored == "" {
				continue
			}
			if item.expires <= now {
				codeExpired = true
				continue
			}
			if item.attempts >= verificationMaxTries {
				continue
			}
			active++
			expected, _ := base64.RawURLEncoding.DecodeString(item.stored)
			matched = hmac.Equal(provided, expected) || matched
		}
		if matched {
			marked, err := tx.Exec(ctx, `UPDATE users SET email_verified = ?, updated_at = ? WHERE id = ? AND email_lower = ?`, true, time.Now().UnixMilli(), userID, strings.ToLower(account.Email))
			if err != nil {
				return fmt.Errorf("auth: mark verified by code: %w", err)
			}
			affected, err := marked.RowsAffected()
			if err != nil {
				return fmt.Errorf("auth: confirm verification code: %w", err)
			}
			if affected == 0 {
				outcome = ErrVerificationInvalid
				return txExecDeleteVerification(ctx, tx, userID)
			}
			if _, err := tx.Exec(ctx, `DELETE FROM email_verifications WHERE user_id = ?`, userID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM email_verification_attempts WHERE user_id = ?`, userID); err != nil {
				return fmt.Errorf("auth: clear verification attempts: %w", err)
			}
			return nil
		}
		if active == 0 {
			if codeExpired {
				outcome = ErrVerificationCodeExpired
			} else {
				outcome = ErrVerificationInvalid
			}
			return nil
		}
		// A resend may briefly leave two codes live. Count the same wrong
		// guess against each usable code for its own cap, while the account
		// budget below prevents replacements from restoring guesses.
		for _, item := range candidates {
			if strings.ToLower(item.email) != strings.ToLower(account.Email) || item.stored == "" || item.expires <= now || item.attempts >= verificationMaxTries {
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE email_verifications SET code_attempts = code_attempts + 1 WHERE id = ? AND user_id = ? AND code_attempts < ?`, item.id, userID, verificationMaxTries); err != nil {
				return fmt.Errorf("auth: count verification code attempt: %w", err)
			}
		}
		attempts++
		if _, err := tx.Exec(ctx,
			`INSERT INTO email_verification_attempts (user_id, attempts, last_attempt_at) VALUES (?, ?, ?)
			 ON CONFLICT (user_id) DO UPDATE SET attempts = excluded.attempts, last_attempt_at = excluded.last_attempt_at`,
			userID, attempts, now); err != nil {
			return fmt.Errorf("auth: count account verification attempt: %w", err)
		}
		if attempts >= verificationMaxTries {
			outcome = ErrVerificationCodeLimited
		} else {
			outcome = ErrVerificationInvalid
		}
		return nil
	})
	if err != nil {
		return err
	}
	if outcome != nil {
		return outcome
	}
	if s.RewardInvite != nil {
		s.RewardInvite(ctx, userID, s.VerificationRequired())
	}
	return nil
}

func verificationAttemptBudget(ctx context.Context, q database.Queryer, userID string, now int64) (int, error) {
	var attempts int
	var lastAttempt int64
	if err := q.QueryRow(ctx,
		`SELECT attempts, last_attempt_at FROM email_verification_attempts WHERE user_id = ?`, userID).
		Scan(&attempts, &lastAttempt); err != nil {
		if database.IsNotFound(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("auth: read account verification attempts: %w", err)
	}
	if now-lastAttempt >= verificationTryWindow.Milliseconds() {
		return 0, nil
	}
	return attempts, nil
}

func txExecDeleteVerification(ctx context.Context, tx *database.Tx, userID string) error {
	_, err := tx.Exec(ctx, `DELETE FROM email_verifications WHERE user_id = ?`, userID)
	return err
}

// Resend issues a new link for an account that has not verified yet.
func (s *Service) Resend(ctx context.Context, siteName string, userID string) error {
	token, err := randomVerificationToken()
	if err != nil {
		return err
	}
	var email string
	err = s.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
			return fmt.Errorf("auth: lock for resend: %w", err)
		}
		account, err := s.users.ByID(ctx, tx, userID)
		if err != nil {
			return err
		}
		if account.EmailVerified {
			return ErrAlreadyVerified
		}
		email = strings.TrimSpace(account.Email)
		if email == "" {
			return ErrNoAddress
		}

		// The newly inserted row is both the SMTP payload's durable credential
		// and the reservation that makes parallel sends wait. Existing rows
		// stay valid until delivery succeeds, so a failed relay loses nothing
		// the account could already use.
		now := time.Now().UnixMilli()
		var latest int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(created_at), 0) FROM email_verifications WHERE user_id = ? AND expires_at > ?`,
			userID, now).Scan(&latest); err != nil {
			return fmt.Errorf("auth: read resend window: %w", err)
		}
		if latest > 0 && time.Since(time.UnixMilli(latest)) < maxOutstandingResend {
			return ErrResendTooSoon
		}
		return s.insertVerification(ctx, tx, userID, email, token, time.Now())
	})
	if err != nil {
		return err
	}

	if err := s.SendVerification(ctx, siteName, email, token); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		cleanupErr := s.db.Tx(cleanupCtx, func(tx *database.Tx) error {
			if _, lockErr := tx.Exec(cleanupCtx,
				`UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); lockErr != nil {
				return fmt.Errorf("auth: lock failed resend cleanup: %w", lockErr)
			}
			if _, deleteErr := tx.Exec(cleanupCtx,
				`DELETE FROM email_verifications WHERE user_id = ? AND id = ?`, userID, digest(token)); deleteErr != nil {
				return fmt.Errorf("auth: clear failed resend: %w", deleteErr)
			}
			return nil
		})
		if cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
		return err
	}

	// Delivery succeeded. The owner may have followed an old link or changed
	// their address while SMTP was running, so reread under the same row lock
	// before retiring credentials. Persistence is detached because the email
	// already left the server even if the browser disconnected meanwhile.
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.db.Tx(commitCtx, func(tx *database.Tx) error {
		if _, err := tx.Exec(commitCtx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
			return err
		}
		current, err := s.users.ByID(commitCtx, tx, userID)
		if err != nil {
			return err
		}
		if current.EmailVerified {
			return nil
		}
		if strings.ToLower(current.Email) != strings.ToLower(email) {
			return ErrVerificationInvalid
		}
		var storedEmail string
		if err := tx.QueryRow(commitCtx,
			`SELECT email FROM email_verifications WHERE user_id = ? AND id = ?`, userID, digest(token)).Scan(&storedEmail); err != nil {
			if database.IsNotFound(err) {
				return ErrVerificationInvalid
			}
			return fmt.Errorf("auth: confirm resent verification: %w", err)
		}
		if strings.ToLower(storedEmail) != strings.ToLower(current.Email) {
			return ErrVerificationInvalid
		}
		if _, err := tx.Exec(commitCtx,
			`DELETE FROM email_verifications WHERE user_id = ? AND id <> ?`, userID, digest(token)); err != nil {
			return fmt.Errorf("auth: retire old verification: %w", err)
		}
		return nil
	})
}

func randomVerificationToken() (string, error) {
	raw := make([]byte, verificationBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: verification token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
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
