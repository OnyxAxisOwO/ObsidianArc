package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

var (
	ErrInvalidCredentials   = errors.New("auth: incorrect username or password")
	ErrAccountDisabled      = errors.New("auth: this account has been disabled")
	ErrRegistrationClosed   = errors.New("auth: registration is closed on this server")
	ErrSignupIPBlocked      = errors.New("auth: too many accounts have been created from this address")
	ErrEmailRequired        = errors.New("auth: an email address is required to register here")
	ErrPasswordUnchanged    = errors.New("auth: the new password is the same as the current one")
	ErrCurrentPasswordWrong = errors.New("auth: current password is incorrect")
)

type Service struct {
	db       *database.DB
	users    *user.Store
	groups   *group.Store
	settings *settings.Service
	sessions *SessionStore
	hasher   *Hasher
	cfg      config.Session
	limiter  *Limiter
	signups  *signupGate
	// Optional. Nil, or configured with no host, means every feature
	// that needs mail reports itself as unavailable rather than
	// failing halfway through.
	mailer *mail.Sender
}

func NewService(
	db *database.DB,
	users *user.Store,
	groups *group.Store,
	set *settings.Service,
	mailer *mail.Sender,
	cfg config.Config,
) *Service {
	return &Service{
		db:       db,
		users:    users,
		groups:   groups,
		settings: set,
		sessions: NewSessionStore(db),
		hasher:   NewHasher(cfg.Password),
		cfg:      cfg.Session,
		limiter:  NewLimiter(),
		signups:  newSignupGate(),
		mailer:   mailer,
	}
}

func (s *Service) Sessions() *SessionStore { return s.sessions }
func (s *Service) Hasher() *Hasher         { return s.hasher }

// RegisterInput is what the sign-up form submits.
type RegisterInput struct {
	Username string
	Email    string
	QQ       string
	Password string
	Nickname string
	IP       string
	UA       string
}

// Register creates an account and signs it in. The first account on an empty
// instance becomes an administrator regardless of whether registration is
// otherwise open, which is what makes a fresh deployment usable without
// environment variables.
//
// The second return is the session token, not the verification one: the link
// is mailed from here, off the request, so a briefly unreachable SMTP server
// does not fail a registration that has already been written.
func (s *Service) Register(ctx context.Context, in RegisterInput) (user.User, string, error) {
	if err := user.ValidateUsername(in.Username); err != nil {
		return user.User{}, "", err
	}
	if err := user.ValidateEmail(in.Email); err != nil {
		return user.User{}, "", err
	}
	if err := user.ValidateQQ(in.QQ); err != nil {
		return user.User{}, "", err
	}
	if err := ValidatePassword(in.Password); err != nil {
		return user.User{}, "", err
	}

	// Everything cheap happens before the expensive thing.
	//
	// Argon2id is deliberately costly — 19 MiB and a slot in a bounded
	// semaphore per call — which makes it a resource an anonymous caller
	// should not be able to spend on a request that was never going to
	// succeed. Hashing first meant a closed instance still paid full price
	// for every attempt, and the signup throttle only started counting
	// after the work was already done.
	//
	// The tx below repeats these checks. This pass is about not doing work;
	// that one is the decision, taken with the row lock that makes it true.
	total, err := s.users.Count(ctx, nil)
	if err != nil {
		return user.User{}, "", err
	}
	if total > 0 {
		if !s.settings.Bool(settings.RegistrationEnabled) {
			return user.User{}, "", ErrRegistrationClosed
		}
		if err := checkEmail(s.settings, in.Email); err != nil {
			return user.User{}, "", err
		}
		if err := checkQQ(s.settings, in.QQ); err != nil {
			return user.User{}, "", err
		}
		if allowed, retryAfter := s.signups.allow(
			s.settings.Int(settings.SignupsPerMinute, 0),
			s.settings.Int(settings.SignupsPerHour, 0),
		); !allowed {
			return user.User{}, "", &SignupThrottleError{RetryAfter: retryAfter}
		}
	}

	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return user.User{}, "", err
	}

	var (
		created      user.User
		verification string
	)
	err = s.db.Tx(ctx, func(tx *database.Tx) error {
		// Serialise the decision about who is first across processes as well
		// as goroutines. Under Postgres' default isolation, two fresh-instance
		// registrations can otherwise both count zero users and both become
		// administrators. Upserting one known settings row takes the same row
		// lock on both supported databases without changing its value.
		if _, err := tx.Exec(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT (key) DO UPDATE SET updated_at = settings.updated_at`,
			settings.RegistrationEnabled, settings.Defaults[settings.RegistrationEnabled],
			time.Now().UnixMilli()); err != nil {
			return fmt.Errorf("auth: lock registration: %w", err)
		}

		total, err := s.users.Count(ctx, tx)
		if err != nil {
			return err
		}
		first := total == 0

		if !first && !s.settings.Bool(settings.RegistrationEnabled) {
			return ErrRegistrationClosed
		}
		// None of the registration controls apply to the first account.
		// It is the one that turns an empty instance into an
		// administered one, and locking someone out of that would leave
		// a deployment with no way in at all.
		if !first {
			if err := checkEmail(s.settings, in.Email); err != nil {
				return err
			}
			if err := checkQQ(s.settings, in.QQ); err != nil {
				return err
			}
			allowed, retryAfter := s.signups.allow(
				s.settings.Int(settings.SignupsPerMinute, 0),
				s.settings.Int(settings.SignupsPerHour, 0),
			)
			if !allowed {
				return &SignupThrottleError{RetryAfter: retryAfter}
			}

			// Per address, and counted in the database inside the lock this
			// transaction already holds — so the count and the insert cannot
			// interleave, a restart does not hand out a fresh allowance, and
			// two instances against one database agree.
			//
			// Unlike the two limits above, this one refuses rather than asks
			// the caller to wait: somebody who has just made ten accounts
			// does not want to hear about a retry.
			if err := s.checkSignupIP(ctx, tx, in.IP); err != nil {
				return err
			}
		}

		usernameTaken, emailTaken, qqTaken, err := s.users.Exists(ctx, tx, in.Username, in.Email, in.QQ)
		if err != nil {
			return err
		}
		if usernameTaken {
			return user.ErrUsernameTaken
		}
		if emailTaken {
			return user.ErrEmailTaken
		}
		if qqTaken {
			return user.ErrQQTaken
		}

		groupID, err := s.registrationGroup(ctx, tx)
		if err != nil {
			return err
		}

		role := user.RoleUser
		if first {
			role = user.RoleAdmin
		}

		// The first account is never held back: it is the one that turns
		// an empty instance into an administered one.
		unverified := !first && s.VerificationRequired()

		created, err = s.users.Create(ctx, tx, user.CreateInput{
			Username:     in.Username,
			Email:        in.Email,
			QQ:           in.QQ,
			PasswordHash: hash,
			Nickname:     in.Nickname,
			Role:         role,
			GroupID:      groupID,
			Status:       user.StatusActive,
			Unverified:   unverified,
			SignupIP:     in.IP,
		})
		if err != nil {
			return err
		}
		if !created.EmailVerified {
			verification, err = s.issueVerification(ctx, tx, created.ID, created.Email)
			if err != nil {
				return err
			}
		}
		// Record while the registration lock is still held. Putting this
		// after commit leaves a scheduling gap in which the next queued
		// registration can pass the throttle before this one is visible.
		s.signups.record()
		return nil
	})
	if err != nil {
		return user.User{}, "", err
	}

	if verification != "" {
		s.mailVerification(ctx, created.Email, verification)
	}

	token, _, err := s.sessions.Create(ctx, created.ID, s.cfg.TTL, in.IP, in.UA)
	if err != nil {
		return user.User{}, "", err
	}
	now := time.Now().UnixMilli()
	_ = s.users.MarkLogin(ctx, created.ID, now)
	created.LastLoginAt = now
	return created, token, nil
}

// UpdateProfile applies the fields an account owns about itself.
//
// It lives here rather than going straight to the store because changing an
// address has to pass the same gate registering with it does. Handing the
// store a new address let a signed-in user walk around both registration
// controls: the domain allowlist an operator had configured, and — worse —
// the confirmation itself, because `email_verified` stayed true for an
// address its owner had never proved they could read.
func (s *Service) UpdateProfile(ctx context.Context, userID string, in user.ProfileUpdate) (user.User, error) {
	var (
		updated      user.User
		verification string
		address      string
	)
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// Whether the address is new is read here and written below, so the
		// owner's row is locked across both: two updates racing would each
		// compare against the address the other is replacing, and the one
		// that commits second could leave the account verified for an
		// address nobody confirmed.
		if _, err := tx.Exec(ctx,
			`UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
			return fmt.Errorf("auth: lock account: %w", err)
		}
		current, err := s.users.ByID(ctx, tx, userID)
		if err != nil {
			return err
		}

		moved := false
		if in.Email != nil {
			address = strings.TrimSpace(*in.Email)
			// Folded the same way the store folds it, which is not the same
			// way EqualFold does. EqualFold applies Unicode simple case
			// folding: it reads "boſs@example.com" and "boss@example.com" as
			// one address, because U+017F folds to 's'. ToLower does not touch
			// U+017F, so the store would have written a different email_lower
			// — a different identity, since that column is what login and the
			// uniqueness index read — while this decided nothing had moved and
			// skipped the allowlist, the withdrawal and the outstanding links.
			// The comparison has to be in the same alphabet as the write.
			moved = strings.ToLower(address) != strings.ToLower(current.Email)
		}
		// Only when it moves. An operator who narrows the allowlist after
		// accounts exist has not asked for those accounts to be frozen out
		// of their own profile form; they have asked that nobody take an
		// address outside it from now on.
		if moved {
			if err := checkEmail(s.settings, address); err != nil {
				return err
			}
		}
		if in.QQ != nil && strings.TrimSpace(*in.QQ) != current.QQ {
			if err := checkQQ(s.settings, *in.QQ); err != nil {
				return err
			}
		}

		if moved {
			// Whatever is outstanding was issued for the address being left
			// behind. Verify no longer writes an address, so an old link can
			// only fail to match — but it is still a link to somewhere this
			// account has been, and there is no reason to leave it open.
			confirmed := address == "" || !s.VerificationRequired()
			if confirmed {
				// An account with no address has nothing to confirm and
				// nothing to hold back — the rule registration keeps in
				// user.Store.Create, and the one this branch used to break:
				// clearing the address marked the account unconfirmed, issued
				// a link for the empty string, tried to post it there, and
				// then refused the resend because there was no address, so the
				// owner was shut out of sending anything until they typed one
				// back in.
				if _, err := tx.Exec(ctx,
					`UPDATE users SET email_verified = ?, updated_at = ? WHERE id = ?`,
					true, time.Now().UnixMilli(), userID); err != nil {
					return fmt.Errorf("auth: settle confirmation: %w", err)
				}
				if _, err := tx.Exec(ctx,
					`DELETE FROM email_verifications WHERE user_id = ?`, userID); err != nil {
					return fmt.Errorf("auth: clear verifications: %w", err)
				}
			} else {
				// One posted link every couple of minutes, counted the same way
				// and for the same reason as the resend button: without it this
				// form is a way to have the server post mail to a stranger as
				// fast as requests can be made, and the default allowlist is
				// empty, so the stranger can be anyone.
				//
				// The limit is on the sending, not on the move. Registration
				// posts a link of its own, so refusing the change instead would
				// mean nobody could correct an address they had just mistyped
				// into the sign-up form.
				var issuedAt int64
				throttled := false
				switch err := tx.QueryRow(ctx,
					`SELECT created_at FROM email_verifications WHERE user_id = ?`, userID).
					Scan(&issuedAt); {
				case err == nil:
					throttled = time.Since(time.UnixMilli(issuedAt)) < maxOutstandingResend
				case !database.IsNotFound(err):
					return fmt.Errorf("auth: read verification: %w", err)
				}

				if _, err := tx.Exec(ctx,
					`UPDATE users SET email_verified = ?, updated_at = ? WHERE id = ?`,
					false, time.Now().UnixMilli(), userID); err != nil {
					return fmt.Errorf("auth: withdraw confirmation: %w", err)
				}
				token, err := s.issueVerification(ctx, tx, userID, address)
				if err != nil {
					return err
				}
				if throttled {
					// The new link is the only valid one, so it is written
					// either way — but it keeps the clock the one before it
					// started, or moving address would be a way to reset the
					// resend limit and post again immediately.
					if _, err := tx.Exec(ctx,
						`UPDATE email_verifications SET created_at = ? WHERE user_id = ?`,
						issuedAt, userID); err != nil {
						return fmt.Errorf("auth: hold the resend window: %w", err)
					}
				} else {
					verification = token
				}
			}
		}

		// Last, so the record it reads back already carries the withdrawn
		// confirmation.
		updated, err = s.users.UpdateProfile(ctx, tx, userID, in)
		return err
	})
	if err != nil {
		return user.User{}, err
	}
	if verification != "" {
		s.mailVerification(ctx, address, verification)
	}
	return updated, nil
}

// mailVerification sends the link without the caller waiting for it.
//
// Detached, because the write it belongs to is already committed: an SMTP
// server that is briefly unreachable must not turn a successful registration
// or profile change into a failure, and nobody should sit through a mail
// handshake to find out their nickname was saved. If it never arrives there
// is a resend button behind the banner.
func (s *Service) mailVerification(ctx context.Context, email, token string) {
	siteName := s.settings.Get(settings.SiteName)
	go func() {
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := s.SendVerification(sendCtx, siteName, email, token); err != nil {
			slog.ErrorContext(sendCtx, "could not send verification mail", "error", err)
		}
	}()
}

// The configured registration group when it still exists, the instance
// default otherwise. A group can be deleted after being named here, and a
// dangling id would leave new users in no group at all.
func (s *Service) registrationGroup(ctx context.Context, q database.Queryer) (string, error) {
	if configured := s.settings.Get(settings.RegistrationGroup); configured != "" {
		if _, err := s.groups.ByID(ctx, q, configured); err == nil {
			return configured, nil
		}
	}
	fallback, err := s.groups.Default(ctx, q)
	if err != nil {
		if errors.Is(err, group.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return fallback.ID, nil
}

type LoginInput struct {
	Identifier string
	Password   string
	IP         string
	UA         string
}

// Login verifies a credential and issues a session.
//
// Every failure path returns the same error, and an unknown account still
// pays for a full Argon2id verification, so neither the message nor the
// timing distinguishes "no such user" from "wrong password".
func (s *Service) Login(ctx context.Context, in LoginInput) (user.User, string, error) {
	attempt, err := s.limiter.Begin(in.IP, in.Identifier)
	if err != nil {
		return user.User{}, "", err
	}
	// Internal errors and cancellation are neither a wrong password nor a
	// success. Explicit outcomes below win because finish is idempotent.
	defer attempt.finish(attemptCancelled)

	account, hash, err := s.users.CredentialsByLogin(ctx, in.Identifier)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			s.hasher.DummyVerify(ctx, in.Password)
			attempt.finish(attemptFailed)
			return user.User{}, "", ErrInvalidCredentials
		}
		return user.User{}, "", err
	}

	ok, needsRehash, err := s.hasher.Verify(ctx, hash, in.Password)
	if err != nil {
		return user.User{}, "", err
	}
	if !ok {
		attempt.finish(attemptFailed)
		return user.User{}, "", ErrInvalidCredentials
	}

	// Checked after verification on purpose: telling an anonymous caller that
	// an account is disabled would confirm the account exists.
	if !account.IsActive() {
		return user.User{}, "", ErrAccountDisabled
	}

	attempt.finish(attemptSucceeded)

	if needsRehash {
		if upgraded, hashErr := s.hasher.Hash(ctx, in.Password); hashErr == nil {
			_ = s.users.SetPasswordHash(ctx, nil, account.ID, upgraded)
		}
	}

	token, _, err := s.sessions.Create(ctx, account.ID, s.cfg.TTL, in.IP, in.UA)
	if err != nil {
		return user.User{}, "", err
	}
	now := time.Now().UnixMilli()
	_ = s.users.MarkLogin(ctx, account.ID, now)
	account.LastLoginAt = now
	return account, token, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.sessions.DeleteByToken(ctx, token)
}

// ChangePassword rotates a credential and invalidates every other session for
// the account, keeping only the one making the change.
func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword, keepSessionID string) error {
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}

	account, err := s.users.ByID(ctx, nil, userID)
	if err != nil {
		return err
	}
	_, hash, err := s.users.CredentialsByLogin(ctx, account.Username)
	if err != nil {
		return err
	}

	ok, _, err := s.hasher.Verify(ctx, hash, currentPassword)
	if err != nil {
		return err
	}
	if !ok {
		return ErrCurrentPasswordWrong
	}
	if same, _, _ := s.hasher.Verify(ctx, hash, newPassword); same {
		return ErrPasswordUnchanged
	}

	updated, err := s.hasher.Hash(ctx, newPassword)
	if err != nil {
		return err
	}

	return s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := s.users.SetPasswordHash(ctx, tx, userID, updated); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`,
			userID, keepSessionID); err != nil {
			return fmt.Errorf("auth: revoke other sessions: %w", err)
		}
		return nil
	})
}

// SetPassword is the administrator's reset: no current password, and every
// session for that account is dropped.
func (s *Service) SetPassword(ctx context.Context, userID, newPassword string) error {
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, newPassword)
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := s.users.SetPasswordHash(ctx, tx, userID, hash); err != nil {
			return err
		}
		return s.sessions.DeleteByUser(ctx, tx, userID)
	})
}

// Authenticate resolves a cookie to its account. A disabled account is
// rejected here, so a session issued before the account was disabled stops
// working on its next request rather than at its next expiry.
func (s *Service) Authenticate(ctx context.Context, token string) (user.User, Session, error) {
	session, err := s.sessions.Get(ctx, token)
	if err != nil {
		return user.User{}, Session{}, err
	}

	account, err := s.users.ByID(ctx, nil, session.UserID)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			_ = s.sessions.DeleteByID(ctx, session.ID)
			return user.User{}, Session{}, ErrSessionNotFound
		}
		return user.User{}, Session{}, err
	}
	if !account.IsActive() {
		return user.User{}, Session{}, ErrAccountDisabled
	}

	if time.Since(time.UnixMilli(session.LastSeenAt)) > s.cfg.TouchInterval {
		_ = s.sessions.Touch(ctx, session.ID, s.cfg.TTL)
	}
	return account, session, nil
}

// --- cookie ------------------------------------------------------------------

func (s *Service) CookieName() string { return s.cfg.CookieName }

func (s *Service) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:  s.cfg.CookieName,
		Value: token,
		Path:  "/",
		// HttpOnly is what keeps the token out of reach of page script, so an
		// XSS bug cannot exfiltrate a session. SameSite=Lax is half the CSRF
		// defence; the Origin check in httpx.SameOrigin is the other half.
		HttpOnly: true,
		Secure:   s.cfg.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.cfg.TTL.Seconds()),
	})
}

func (s *Service) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Service) TokenFrom(r *http.Request) string {
	cookie, err := r.Cookie(s.cfg.CookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// checkSignupIP enforces the per-address registration limit.
//
// Zero is off, and stays off: an operator who has not set a number has not
// asked for this, and a limit guessed on their behalf is one that locks out a
// university or an office behind a single address.
func (s *Service) checkSignupIP(ctx context.Context, q database.Queryer, ip string) error {
	limit := s.settings.Int(settings.SignupsPerIP, 0)
	if limit <= 0 || strings.TrimSpace(ip) == "" {
		return nil
	}

	minutes := s.settings.Int(settings.SignupsIPWindowMin, 60)
	if minutes <= 0 {
		minutes = 60
	}
	since := time.Now().Add(-time.Duration(minutes) * time.Minute).UnixMilli()

	count, err := s.users.CountFromIP(ctx, q, ip, since)
	if err != nil {
		return err
	}
	// The limit is how many may exist, so the one that would make it the
	// limit-plus-first is the one refused.
	if count >= limit {
		return ErrSignupIPBlocked
	}
	return nil
}
