package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

var (
	ErrInvalidCredentials   = errors.New("auth: incorrect username or password")
	ErrAccountDisabled      = errors.New("auth: this account has been disabled")
	ErrRegistrationClosed   = errors.New("auth: registration is closed on this server")
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
}

func NewService(
	db *database.DB,
	users *user.Store,
	groups *group.Store,
	set *settings.Service,
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
	}
}

func (s *Service) Sessions() *SessionStore { return s.sessions }
func (s *Service) Hasher() *Hasher         { return s.hasher }

// RegisterInput is what the sign-up form submits.
type RegisterInput struct {
	Username string
	Email    string
	Password string
	Nickname string
	IP       string
	UA       string
}

// Register creates an account and signs it in. The first account on an empty
// instance becomes an administrator regardless of whether registration is
// otherwise open, which is what makes a fresh deployment usable without
// environment variables.
func (s *Service) Register(ctx context.Context, in RegisterInput) (user.User, string, error) {
	if err := user.ValidateUsername(in.Username); err != nil {
		return user.User{}, "", err
	}
	if err := user.ValidateEmail(in.Email); err != nil {
		return user.User{}, "", err
	}
	if err := ValidatePassword(in.Password); err != nil {
		return user.User{}, "", err
	}

	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return user.User{}, "", err
	}

	var created user.User
	err = s.db.Tx(ctx, func(tx *database.Tx) error {
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
			allowed, retryAfter := s.signups.allow(
				s.settings.Int(settings.SignupsPerMinute, 0),
				s.settings.Int(settings.SignupsPerHour, 0),
			)
			if !allowed {
				return &SignupThrottleError{RetryAfter: retryAfter}
			}
		}

		usernameTaken, emailTaken, err := s.users.Exists(ctx, tx, in.Username, in.Email)
		if err != nil {
			return err
		}
		if usernameTaken {
			return user.ErrUsernameTaken
		}
		if emailTaken {
			return user.ErrEmailTaken
		}

		groupID, err := s.registrationGroup(ctx, tx)
		if err != nil {
			return err
		}

		role := user.RoleUser
		if first {
			role = user.RoleAdmin
		}

		created, err = s.users.Create(ctx, tx, user.CreateInput{
			Username:     in.Username,
			Email:        in.Email,
			PasswordHash: hash,
			Nickname:     in.Nickname,
			Role:         role,
			GroupID:      groupID,
			Status:       user.StatusActive,
		})
		return err
	})
	if err != nil {
		return user.User{}, "", err
	}

	// Counted only once the account exists, so a rejected attempt does
	// not spend the next person's place in the window.
	s.signups.record()

	token, _, err := s.sessions.Create(ctx, created.ID, s.cfg.TTL, in.IP, in.UA)
	if err != nil {
		return user.User{}, "", err
	}
	now := time.Now().UnixMilli()
	_ = s.users.MarkLogin(ctx, created.ID, now)
	created.LastLoginAt = now
	return created, token, nil
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
	if err := s.limiter.Allow(in.IP, in.Identifier); err != nil {
		return user.User{}, "", err
	}

	account, hash, err := s.users.CredentialsByLogin(ctx, in.Identifier)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			s.hasher.DummyVerify(ctx, in.Password)
			s.limiter.Fail(in.IP, in.Identifier)
			return user.User{}, "", ErrInvalidCredentials
		}
		return user.User{}, "", err
	}

	ok, needsRehash, err := s.hasher.Verify(ctx, hash, in.Password)
	if err != nil {
		return user.User{}, "", err
	}
	if !ok {
		s.limiter.Fail(in.IP, in.Identifier)
		return user.User{}, "", ErrInvalidCredentials
	}

	// Checked after verification on purpose: telling an anonymous caller that
	// an account is disabled would confirm the account exists.
	if !account.IsActive() {
		return user.User{}, "", ErrAccountDisabled
	}

	s.limiter.Reset(in.IP, in.Identifier)

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
