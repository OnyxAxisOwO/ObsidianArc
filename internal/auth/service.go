package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugingate"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/pow"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// AccountDisabledError is returned when an authentication attempt is made
// against an account marked StatusDisabled. It carries the optional ban reason
// supplied by an administrator so user-facing refusals can explain why.
type AccountDisabledError struct {
	Reason string
}

func (e *AccountDisabledError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("auth: this account has been disabled: %s", e.Reason)
	}
	return "auth: this account has been disabled"
}

func (e *AccountDisabledError) Is(target error) bool {
	return target == ErrAccountDisabled
}

var (
	ErrInvalidCredentials         = errors.New("auth: incorrect username or password")
	ErrAccountDisabled            = errors.New("auth: this account has been disabled")
	ErrRegistrationClosed         = errors.New("auth: registration is closed on this server")
	ErrThirdPartyOnlyRegistration = errors.New("auth: registration is only permitted via third-party providers")
	ErrOIDCOnlyRegistration       = ErrThirdPartyOnlyRegistration
	ErrSignupIPBlocked            = errors.New("auth: too many accounts have been created from this address")
	// The review said no. The words a visitor sees are the operator's, set in
	// the security screen; this only carries the fact.
	ErrSignupRefused        = errors.New("auth: this registration was not accepted")
	ErrEmailRequired        = errors.New("auth: an email address is required to register here")
	ErrPasswordUnchanged    = errors.New("auth: the new password is the same as the current one")
	ErrCurrentPasswordWrong = errors.New("auth: current password is incorrect")
	// Nothing was typed where an account with a password has to confirm it.
	// Apart from a wrong one so the screen can ask instead of accuse, and so
	// an empty field never spends a guess from the budget.
	ErrPasswordRequired = errors.New("auth: your current password is required for this")
	// An account opened through a provider has no password to prove. Its
	// first one is set from a session that has only just signed in, because
	// a stolen cookie for such an account would otherwise set a password the
	// owner cannot undo without an administrator.
	ErrReauthRequired = errors.New("auth: a first password needs a sign-in made just now")
	// One code for both, deliberately: see consumeInvite.
	ErrInviteRequired = errors.New("auth: an invite code is required to register here")
	ErrInviteInvalid  = errors.New("auth: that invite code is not valid")
	// A fresh instance can gain its first account between preflight and the
	// registration row lock. Leave the transaction and screen that second
	// account's address before retrying, so it cannot slip past the check.
	errNeedsEmailScreening = errors.New("auth: email screening must run before the transaction")
	// The password check is an Argon2 verification, which does not belong
	// inside a transaction; UpdateProfile learns inside the lock that the
	// address is moving, leaves, checks, and comes back.
	errNeedsPasswordCheck = errors.New("auth: the password must be confirmed before the transaction")
)

// InviteGrant is what consuming an invite code hands back to Register and
// Provision: which code was spent, who gets credited for the invite (empty
// for an admin-issued code), and the group membership it carries.
//
// A local type rather than internal/invite's own, because that package's
// HTTP handlers import this one (auth.RequireUser, auth.MustUser) — a
// two-way import between the two would not compile. server.go, which
// imports both, translates between this shape and invite.Grant in the hooks
// below.
type InviteGrant struct {
	CodeID    string
	OwnerID   string
	GroupID   string
	GroupDays int64
}

type Service struct {
	db    *database.DB
	users *user.Store
	// Which plugins are on, for their guards and their sign-in blocks.
	gate     plugingate.Gate
	groups   *group.Store
	settings *settings.Service
	sessions *SessionStore
	hasher   *Hasher
	cfg      config.Session
	limiter  *Limiter
	signups  *signupGate
	// Set by the wiring when the operator has switched a challenge on. The
	// zero value is off, so a build that never wires it up simply has no
	// challenge rather than a broken one.
	Challenge      turnstile.Gate
	LoginChallenge turnstile.Gate
	// A plugin's checks, in front of the same two doors — see AddGuard —
	// and how sign-up treats each plugin account field — see SetFieldRule.
	// Written at setup, and again when a plugin is installed or removed
	// while the server runs, so every read goes through extMu.
	extMu      sync.RWMutex
	guards     map[string][]Guard
	fieldRules map[string]func() string
	// Self-developed proof-of-work manager. Nil is off.
	PoW *pow.Manager
	// Challenge failure hook for recording to security events.
	OnChallengeFailure func(ctx context.Context, event, ip, username, reason string)
	// Asks a model whether a sign-up looks like a person. Nil is off.
	//
	// A function rather than the reviewer itself: the review needs a model,
	// a provider and an adapter registry, and auth has no business knowing
	// about any of them. A call failure still carries the mode's fallback
	// decision; the error exists so the wiring can report why it was needed.
	ReviewSignup func(ctx context.Context, in RegisterInput, fromAddress int) (SignupReview, error)
	// A paid disposable-address lookup, configured by the administrator.
	// Call before account writes, never from inside a transaction.
	ScreenEmail func(context.Context, string) error
	// Called after a review has a durable outcome, including a refusal where
	// no account was created. Recording is best-effort and must not turn an
	// audit failure into a registration failure.
	OnSignupReview func(context.Context, RegisterInput, *user.User, SignupReview)
	// Optional. Nil, or configured with no host, means every feature
	// that needs mail reports itself as unavailable rather than
	// failing halfway through.
	mailer *mail.Sender

	// Two-step sign-in. The box seals the secrets authenticator apps hold;
	// the key signs remembered browsers and keys recovery-code digests.
	// Both are nil on a build with no instance secret, where two-step
	// sign-in reports itself unavailable rather than inventing a key.
	twoFactorBox    *secret.Box
	twoFactorKey    []byte
	verificationKey []byte
	// Its own budget, apart from the password limiter's: see spendCode.
	codes *Limiter
	// The budget for guesses at an invite code, per address. Its own limiter
	// for the reason codes has one: a wrong code is not a wrong password, and
	// spending a sign-in's allowance on it would lock the same person out of
	// both. See spendInvite.
	invites *Limiter
	// Told when the second step is switched on or off, or a recovery code
	// is spent. Nil records nothing.
	OnTwoFactor func(context.Context, TwoFactorEvent)
	// The caller's address as the proxy settings resolve it, for the check
	// that ends a backoffice visit when the network changes. Set by the
	// wiring, which owns the proxy trust; nil skips that check.
	ClientIP func(*http.Request) string
	// Told after a sign-in from a device this account has not used before.
	// Called detached (see RecordDevice) so a slow or failing subscriber —
	// the security log, a notification — cannot turn a sign-in that already
	// succeeded into one that fails. Nil records nothing.
	OnNewDevice func(context.Context, NewDeviceEvent)
	// Ends what the account holds with applications that signed in through
	// this instance. The identity provider lives in internal/idp, which imports
	// this package, so the wiring supplies the call. It runs inside the
	// transaction that replaces the password, so a failure there undoes the
	// change as well. Nil is off.
	RevokeIssued func(ctx context.Context, q database.Queryer, userID string) error

	// Invite codes. Three hooks rather than one dependency on
	// internal/invite — see InviteGrant's comment for why this package
	// cannot import that one. Nil is only ever true in a test that has no
	// reason to exercise invites; every real deployment wires all three from
	// server.go, the same seam ReviewSignup and OnNewDevice use.
	//
	// ConsumeInvite spends one use inside the caller's transaction — see
	// invite.Store.Consume. RecordInviteUse writes the row Consume's spend
	// is remembered by, in the same transaction, once the new account's id
	// is known. RewardInvite is told about an account that may now qualify
	// for its inviter's reward — after Register commits, and again from
	// Verify — and resolves and logs for itself; it has nothing to hand back
	// because a reward is best-effort by design (see invite.Store.Reward).
	ConsumeInvite   func(ctx context.Context, tx *database.Tx, code string) (*InviteGrant, error)
	RecordInviteUse func(ctx context.Context, tx *database.Tx, grant InviteGrant, userID string) error
	RewardInvite    func(ctx context.Context, userID string, verificationRequired bool)

	// Called between a sign-in computing its upgraded hash and writing it: the
	// window a password change has to be able to land in. Nil outside tests,
	// which set it to make that change at exactly that point.
	beforeRehashWrite func()
	// Called by VerifyCredential between reading the stored hash and checking the
	// password against it. A change landing there is the case the fingerprint it
	// returns must survive: the check passes against the hash that was read, and
	// the fingerprint has to describe that same hash. Nil outside tests.
	afterCredentialRead func()
}

func NewService(
	db *database.DB,
	users *user.Store,
	groups *group.Store,
	set *settings.Service,
	mailer *mail.Sender,
	cfg config.Config,
) *Service {
	service := &Service{
		db:       db,
		users:    users,
		groups:   groups,
		settings: set,
		sessions: NewSessionStore(db, users),
		hasher:   NewHasher(cfg.Password),
		cfg:      cfg.Session,
		limiter:  NewLimiter(),
		codes:    NewLimiter(),
		invites:  NewLimiter(),
		signups:  newSignupGate(),
		mailer:   mailer,
	}
	if len(cfg.SecretKey) > 0 {
		verificationKey, verificationErr := secret.DeriveKey(cfg.SecretKey, secret.PurposeEmailCode)
		if verificationErr == nil {
			service.verificationKey = verificationKey
		}
		box, boxErr := secret.New(cfg.SecretKey, secret.PurposeTwoFactor)
		key, keyErr := secret.DeriveKey(cfg.SecretKey, secret.PurposeTwoFactorDigest)
		if boxErr == nil && keyErr == nil {
			service.twoFactorBox, service.twoFactorKey = box, key
		}
	}
	return service
}

func (s *Service) Sessions() *SessionStore { return s.sessions }
func (s *Service) Hasher() *Hasher         { return s.hasher }

// RegisterInput is what the sign-up form submits.
type RegisterInput struct {
	Username string
	Email    string
	// Plugin account fields, by key — see user.DefineField.
	Fields   map[string]string
	Password string
	Nickname string
	IP       string
	UA       string
	// Turnstile's token, when the operator has switched the challenge on.
	Turnstile string
	// Each plugin guard's token, by guard name — see AddGuard.
	Guards map[string]string
	// PoW solution, when proof-of-work challenge is required.
	PoW *pow.Solution
	// Empty unless this instance's registration mode asks for one — see
	// consumeInvite. Optional even where it is not required: a code applied
	// in open mode still seats the account in its group.
	InviteCode string
}

type SignupDecision string

const (
	SignupAllow    SignupDecision = "allow"
	SignupRestrict SignupDecision = "restrict"
	SignupRefuse   SignupDecision = "refuse"
)

type SignupReview struct {
	Ran             bool
	Decision        SignupDecision
	Reason          string
	RestrictedUntil int64
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
	if _, err := s.users.CheckFields(in.Fields); err != nil {
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
	review := SignupReview{Decision: SignupAllow}
	// The place this request holds in the invite guessing budget, when it carries
	// a code. It is kept until the request returns, so the paid review and the
	// hash below only run while the place is held; see ReserveInviteGuess.
	var guess *Attempt
	if total > 0 {
		if !s.settings.Bool(settings.RegistrationEnabled) {
			return user.User{}, "", ErrRegistrationClosed
		}
		if s.isThirdPartyOnlySignup() && s.hasOAuthProviders() {
			return user.User{}, "", ErrThirdPartyOnlyRegistration
		}
		if err := checkEmail(s.settings, in.Email); err != nil {
			return user.User{}, "", err
		}
		if s.VerificationRequired() && strings.TrimSpace(in.Email) == "" {
			return user.User{}, "", ErrEmailRequired
		}
		if err := s.checkFields(in.Fields); err != nil {
			return user.User{}, "", err
		}
		// Free, so it happens before the throttle counts anything: a request
		// with no code on an invite-only instance was never going to
		// succeed, and should not spend part of the per-minute allowance
		// finding that out.
		if strings.TrimSpace(in.InviteCode) == "" && s.settings.Bool(settings.InvitesRequired) {
			return user.User{}, "", ErrInviteRequired
		}
		// A code is a guess until it checks out, and the paid review, the screening
		// and the hash below are all spent on a guess before anything learns whether
		// it was right. The place in the budget is taken here, before any of that,
		// and kept until this request returns. Requests from one address that arrive
		// together share the budget while they run, instead of all getting past it
		// before any of them has been counted; the transaction below counts a wrong
		// code against this same place.
		if strings.TrimSpace(in.InviteCode) != "" {
			held, err := s.ReserveInviteGuess(in.IP)
			if err != nil {
				return user.User{}, "", err
			}
			guess = held
			defer guess.Cancelled()
		}
		if allowed, retryAfter := s.signups.allow(
			s.settings.Int(settings.SignupsPerMinute, 0),
			s.settings.Int(settings.SignupsPerHour, 0),
		); !allowed {
			return user.User{}, "", &SignupThrottleError{RetryAfter: retryAfter}
		}
	}

	// Before the transaction, and before hashing: these are challenge verifications.
	// Hashing is deliberate work and there is no reason to do it for a request
	// that has already failed.
	//
	// After the first-account check above, so a fresh instance is never
	// locked out of its own setup by a challenge nobody could pass yet.
	if total > 0 {
		captchaMode := s.settings.RegistrationCaptchaMode()

		// 1. Proof-of-work challenge verification (PoW -> Turnstile -> AI Review)
		if captchaMode == settings.CaptchaModePoW || captchaMode == settings.CaptchaModeBoth {
			if err := s.verifyProof(ctx, in.IP, in.Username, in.PoW); err != nil {
				return user.User{}, "", err
			}
		}

		// 2. Cloudflare Turnstile challenge verification
		if captchaMode == settings.CaptchaModeTurnstile || captchaMode == settings.CaptchaModeBoth {
			if err := s.Challenge.Check(ctx, in.Turnstile, in.IP); err != nil {
				if s.OnChallengeFailure != nil {
					s.OnChallengeFailure(ctx, "turnstile_challenge", in.IP, in.Username, "Turnstile 人机验证未通过")
				}
				return user.User{}, "", err
			}
		}

		// 3. A plugin's guards. A refusal ends it here; a restriction — a
		// guard that let the request through without being sure of it — is
		// carried into the review below, where it becomes the same middle
		// answer the AI reviewer has: the account exists, the programmatic
		// surface stays closed.
		guarded, err := s.runGuards(ctx, GuardRegister, in.Guards, in.IP, in.Username)
		if err != nil {
			return user.User{}, "", err
		}

		if s.PoW != nil && s.PoW.Tracker() != nil {
			s.PoW.Tracker().RecordAttempt(in.IP, time.Now())
		}
		// This pass only saves paid screening and review calls for an address
		// already held here. The transaction still repeats the uniqueness
		// check under the registration lock before writing the account.
		usernameTaken, emailTaken, heldField, err := s.users.Exists(ctx, nil, in.Username, in.Email, in.Fields)
		if err != nil {
			return user.User{}, "", err
		}
		if usernameTaken {
			return user.User{}, "", user.ErrUsernameTaken
		}
		if emailTaken {
			return user.User{}, "", user.ErrEmailTaken
		}
		if heldField != "" {
			return user.User{}, "", takenError(heldField)
		}

		// Last of the gates and outside the transaction, for the same two
		// reasons: it is a call to a provider, and it is the slowest thing
		// here. Everything cheap has already had its chance to refuse.
		if s.ReviewSignup != nil {
			seen, err := s.countRecentFromIP(ctx, in.IP)
			if err != nil {
				return user.User{}, "", err
			}
			review, _ = s.ReviewSignup(ctx, in, seen)
			switch review.Decision {
			case SignupAllow, SignupRestrict, SignupRefuse:
			default:
				review.Decision = SignupRestrict
				review.Reason = "审查未返回有效决策"
			}
			if review.Decision == SignupRefuse {
				s.recordSignupReview(ctx, in, nil, review)
				return user.User{}, "", ErrSignupRefused
			}
		}
		// A guard's middle band, applied whatever the AI review said — and
		// whether or not it ran. A refusal from either side still wins:
		// restricted is the softer answer, and a refusal was already
		// returned above.
		if guarded.Restrict && review.Decision == SignupAllow {
			review.Decision = SignupRestrict
			review.Reason = guarded.Reason
		}
	}
	screened := false
	if total > 0 {
		if err := s.CheckRegistrationEmail(ctx, in.Email); err != nil {
			return user.User{}, "", err
		}
		screened = true
	}

	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return user.User{}, "", err
	}

	var (
		created      user.User
		verification string
	)
	for {
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
			if !first && s.isThirdPartyOnlySignup() && s.hasOAuthProviders() {
				return ErrThirdPartyOnlyRegistration
			}
			// None of the registration controls apply to the first account.
			// It is the one that turns an empty instance into an
			// administered one, and locking someone out of that would leave
			// a deployment with no way in at all.
			if !first {
				if err := checkEmail(s.settings, in.Email); err != nil {
					return err
				}
				if !screened {
					return errNeedsEmailScreening
				}
				if s.VerificationRequired() && strings.TrimSpace(in.Email) == "" {
					return ErrEmailRequired
				}
				if err := s.checkFields(in.Fields); err != nil {
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

			// Consumed here, inside the same transaction as the account it is
			// spent for: a registration that fails for any other reason below —
			// a taken username, a race lost on the signup IP count — rolls the
			// spend back with it, and the code is exactly as good afterwards as
			// it was before this request touched it. Skipped for the first
			// account along with every other registration control, above.
			var grant *InviteGrant
			if !first {
				grant, err = s.spendInvite(ctx, tx, in.InviteCode, in.IP, guess)
				if err != nil {
					return err
				}
			}

			usernameTaken, emailTaken, heldField, err := s.users.Exists(ctx, tx, in.Username, in.Email, in.Fields)
			if err != nil {
				return err
			}
			if usernameTaken {
				return user.ErrUsernameTaken
			}
			if emailTaken {
				return user.ErrEmailTaken
			}
			if heldField != "" {
				return takenError(heldField)
			}

			groupID, err := s.registrationGroup(ctx, tx)
			if err != nil {
				return err
			}
			// An admin code's group replaces the instance default rather than
			// combining with it — a partner's trial is a specific group chosen
			// for that link, not a suggestion layered onto whatever registration
			// would otherwise have picked.
			if grant != nil && grant.GroupID != "" {
				groupID = grant.GroupID
			}

			role := user.RoleUser
			if first {
				role = user.RoleSuperAdmin
			}

			// The first account is never held back: it is the one that turns
			// an empty instance into an administered one.
			unverified := !first && s.VerificationRequired()

			created, err = s.users.Create(ctx, tx, user.CreateInput{
				Username:             in.Username,
				Email:                in.Email,
				Fields:               in.Fields,
				PasswordHash:         hash,
				Nickname:             in.Nickname,
				Role:                 role,
				GroupID:              groupID,
				Status:               user.StatusActive,
				Unverified:           unverified,
				SignupIP:             in.IP,
				SignupUserAgent:      in.UA,
				APIRestricted:        review.Decision == SignupRestrict,
				APIRestrictedUntil:   review.RestrictedUntil,
				APIRestrictionSource: restrictionSource(review.Decision),
			})
			if err != nil {
				return err
			}
			groupExpiresAt, err := s.applyInvite(ctx, tx, grant, created.ID)
			if err != nil {
				return err
			}
			created.GroupExpiresAt = groupExpiresAt
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
		if !errors.Is(err, errNeedsEmailScreening) {
			break
		}
		if err := s.CheckRegistrationEmail(ctx, in.Email); err != nil {
			return user.User{}, "", err
		}
		screened = true
	}
	if err != nil {
		return user.User{}, "", err
	}
	s.recordSignupReview(ctx, in, &created, review)

	if verification != "" {
		s.mailVerification(ctx, created.Email, verification)
	}
	// Best-effort and after commit, the same as everything else here: the
	// account exists either way, and a reward this account itself earned
	// nobody by inviting is not this request's problem to retry. A no-op
	// for the overwhelming majority of registrations, which used no
	// personal code at all — see invite.Store.Reward.
	if s.RewardInvite != nil {
		s.RewardInvite(ctx, created.ID, s.VerificationRequired())
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

func restrictionSource(decision SignupDecision) string {
	if decision == SignupRestrict {
		return "signup_review"
	}
	return ""
}

func (s *Service) recordSignupReview(
	ctx context.Context, in RegisterInput, account *user.User, review SignupReview,
) {
	if s.OnSignupReview == nil || !review.Ran {
		return
	}
	// The decision remains useful if the browser goes away just as review
	// finishes. Bound the detached write so an audit problem cannot hold the
	// registration path indefinitely.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	s.OnSignupReview(recordCtx, in, account, review)
}

// UpdateProfile applies the fields an account owns about itself.
//
// It lives here rather than going straight to the store because changing an
// address has to pass the same gate registering with it does. Handing the
// store a new address let a signed-in user walk around both registration
// controls: the domain allowlist an operator had configured, and — worse —
// the confirmation itself, because `email_verified` stayed true for an
// address its owner had never proved they could read.
//
// Moving the address also takes the account's current password, where it has
// one. The address is where a reset link goes, so a session alone — which is
// all a stolen cookie is — must not be able to repoint it. An account opened
// through a provider has no password to ask for and is asked for nothing.
func (s *Service) UpdateProfile(ctx context.Context, userID string, in user.ProfileUpdate, currentPassword string) (user.User, error) {
	var (
		updated      user.User
		verification string
		address      string
	)
	screened := false
	passwordChecked := false
	var err error
	for {
		err = s.db.Tx(ctx, func(tx *database.Tx) error {
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
				if !passwordChecked {
					return errNeedsPasswordCheck
				}
				if err := checkEmail(s.settings, address); err != nil {
					return err
				}
				// Otherwise an unverified account could clear its address,
				// become verified by the no-address rule, and bypass the gate.
				if address == "" && s.VerificationRequired() {
					return ErrEmailRequired
				}
				if err := user.ValidateEmail(address); err != nil {
					return err
				}
				if address != "" && !screened {
					return errNeedsEmailScreening
				}
			}
			// Only a value being changed answers to the current rules: a
			// field that became required after this account opened does not
			// lock the owner out of saving their nickname.
			changed := map[string]string{}
			for key, value := range in.Fields {
				if strings.TrimSpace(value) != current.Fields[key] {
					changed[key] = value
				}
			}
			if len(changed) > 0 {
				if _, err := s.users.CheckFields(changed); err != nil {
					return err
				}
				for key, value := range changed {
					if strings.TrimSpace(value) == "" && s.FieldRule(key) == FieldRequired {
						return &user.FieldError{Key: key, Reason: user.ErrFieldRequired}
					}
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
		if errors.Is(err, errNeedsPasswordCheck) {
			if err := s.ConfirmPassword(ctx, userID, currentPassword); err != nil {
				return user.User{}, err
			}
			passwordChecked = true
			continue
		}
		if !errors.Is(err, errNeedsEmailScreening) {
			break
		}
		if err := s.CheckRegistrationEmail(ctx, address); err != nil {
			return user.User{}, err
		}
		screened = true
	}
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

// consumeInvite enforces the registration mode and, when a code was given,
// spends it through the ConsumeInvite hook inside the caller's transaction.
//
// An empty code is not itself an error unless InvitesRequired says it must
// not be: open mode's code is optional, and this is the one place that
// distinction is made, so Register and Provision do not have to agree on it
// twice. Every failure from the hook collapses to ErrInviteInvalid — see
// invite.Store.Consume for why one answer covers all of them — and a nil
// hook with a non-empty code is treated the same way, since a caller handed
// a code this instance has no invite package wired in to check.
func (s *Service) consumeInvite(ctx context.Context, tx *database.Tx, code string) (*InviteGrant, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		if s.settings.Bool(settings.InvitesRequired) {
			return nil, ErrInviteRequired
		}
		return nil, nil
	}
	if s.ConsumeInvite == nil {
		return nil, ErrInviteInvalid
	}
	grant, err := s.ConsumeInvite(ctx, tx, code)
	if err != nil {
		return nil, ErrInviteInvalid
	}
	return grant, nil
}

// spendInvite is consumeInvite for a code that arrives on a request somebody
// may be guessing with. Every wrong code is a guess, and guesses from one
// address are counted and slowed the way a wrong password is, so a short code
// cannot be walked through at the speed of the registration form. A code that
// works is not counted, and does not clear the count either: only time does.
//
// guess is the place the caller reserved for this request (ReserveInviteGuess),
// and a wrong code is recorded against it. A nil guess means the caller reserved
// none, so one is taken here for the code alone. A request takes one place and no
// more: a second would count the request's own hold against its own guess, and a
// run of paid reviews would then never be stopped.
//
// It runs inside the caller's transaction, but the limiter is in memory and is
// not rolled back with it. That is the point: a transaction that refuses a guess
// has to leave the count standing.
func (s *Service) spendInvite(ctx context.Context, tx *database.Tx, code, ip string, guess *Attempt) (*InviteGrant, error) {
	if strings.TrimSpace(code) == "" {
		return s.consumeInvite(ctx, tx, code)
	}
	if guess == nil {
		held, err := s.ReserveInviteGuess(ip)
		if err != nil {
			return nil, err
		}
		defer held.Cancelled()
		guess = held
	}
	grant, err := s.consumeInvite(ctx, tx, code)
	if errors.Is(err, ErrInviteInvalid) {
		guess.Failed()
	}
	return grant, err
}

// ReserveInviteGuess takes the address's place in the invite guessing budget for
// one request, and refuses when the address has used it up. The place is held
// until the caller finishes with it: Failed when the code it was taken for is
// wrong, and Cancelled otherwise. A code that works is not a guess, and a request
// that stopped before its code was checked made none. A caller takes the place
// before any paid work and holds it to the end of the request, so a burst of
// guesses from one address cannot all get past the budget before any of them has
// been counted.
func (s *Service) ReserveInviteGuess(ip string) (*Attempt, error) {
	return s.invites.Begin(inviteBudgetKey(ip), "")
}

// inviteBudgetKey is the address a guess is counted against. An address the
// server could not read is one shared budget rather than none: a limit that an
// empty address switched off would be a way round the limit.
func inviteBudgetKey(ip string) string {
	if ip == "" {
		return "unknown"
	}
	return ip
}

// applyInvite finishes what consumeInvite started, once the account it was
// spent for exists: the group days an admin code carries become an expiry
// on the fresh row (permanent membership — group_days 0 — needs no write,
// since that is the column's own default), and the use is recorded through
// RecordInviteUse. It returns the expiry written, if any, so the caller can
// carry it onto the in-memory record it is about to hand back — group_id
// was already applied by the caller, onto CreateInput, before Create ran.
func (s *Service) applyInvite(ctx context.Context, tx *database.Tx, grant *InviteGrant, userID string) (int64, error) {
	if grant == nil {
		return 0, nil
	}
	var expiresAt int64
	if grant.GroupID != "" && grant.GroupDays > 0 {
		expiresAt = time.Now().Add(time.Duration(grant.GroupDays) * 24 * time.Hour).UnixMilli()
		if _, err := tx.Exec(ctx, `UPDATE users SET group_expires_at = ? WHERE id = ?`, expiresAt, userID); err != nil {
			return 0, fmt.Errorf("auth: apply invite group expiry: %w", err)
		}
	}
	if s.RecordInviteUse != nil {
		if err := s.RecordInviteUse(ctx, tx, *grant, userID); err != nil {
			return 0, fmt.Errorf("auth: record invite use: %w", err)
		}
	}
	return expiresAt, nil
}

type LoginInput struct {
	Turnstile string
	// Each plugin guard's token, by guard name — see AddGuard.
	Guards     map[string]string
	Identifier string
	Password   string
	IP         string
	UA         string
	// The remembered-browser cookie, when there is one. It stands in for the
	// code, never for the password.
	Remembered string
}

// Login verifies a credential and issues a session.
//
// Every failure path returns the same error, and an unknown account still
// pays for a full Argon2id verification, so neither the message nor the
// timing distinguishes "no such user" from "wrong password".
//
// An account with two-step sign-in comes back as *SecondFactorRequired
// carrying a pending token: the password was right, and nothing is open yet.
func (s *Service) Login(ctx context.Context, in LoginInput) (user.User, string, error) {
	if err := s.LoginChallenge.Check(ctx, in.Turnstile, in.IP); err != nil {
		return user.User{}, "", err
	}
	// A guard's middle band does not apply here: there is no per-request
	// second factor to hand a "maybe" at the door, and locking an account out
	// of its own sign-in costs more than the occasional marginal one costs
	// the instance. Only a refusal refuses.
	if _, err := s.runGuards(ctx, GuardLogin, in.Guards, in.IP, in.Identifier); err != nil {
		return user.User{}, "", err
	}

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
			attempt.finish(attemptFailedUnknown)
			return user.User{}, "", ErrInvalidCredentials
		}
		return user.User{}, "", err
	}

	ok, needsRehash, err := s.hasher.Verify(ctx, hash, in.Password)
	if errors.Is(err, ErrInvalidHash) {
		// An account opened through a provider has no password, and a
		// password is what this form checks. Answered exactly as a wrong one
		// is, and made to cost the same, so the form cannot be used to find
		// out which accounts sign in with GitHub.
		s.hasher.DummyVerify(ctx, in.Password)
		attempt.finish(attemptFailed)
		return user.User{}, "", ErrInvalidCredentials
	}
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
		return user.User{}, "", &AccountDisabledError{Reason: account.BanReason}
	}

	attempt.finish(attemptSucceeded)

	if needsRehash {
		s.rehash(ctx, account.ID, hash, in.Password)
	}

	token, err := s.secondStep(ctx, account, in.Remembered, in.IP, in.UA)
	if err != nil {
		return user.User{}, "", err
	}
	account.LastLoginAt = time.Now().UnixMilli()
	return account, token, nil
}

// VerifyCredential answers "is this the right password for this account",
// and nothing else.
//
// It is Login without the two things that only make sense in a browser: the
// Turnstile gate, which no SSH client can solve, and the session cookie,
// which a console session has no use for. Everything that protects the
// credential itself is kept and deliberately shared with Login — the plugin
// login guards, the same attempt limiter, so guessing over SSH and guessing
// over the sign-in form count against one budget rather than two, and the same
// dummy verification, so an unknown account costs the same wall-clock as a
// known one. A guard that refuses the sign-in form refuses this door too, or
// the operator's refusal is one password away from being ignored.
//
// It also returns the account's credential fingerprint (see
// CredentialFingerprint), taken from the same read of the stored hash that the
// password was checked against. A second read after the check would sit behind
// the whole verification, and a change made in that span would pair the new
// fingerprint with the old password.
//
// Every failure returns ErrInvalidCredentials. A caller that is about to tell
// a stranger whether an account exists is the reason.
func (s *Service) VerifyCredential(ctx context.Context, identifier, password, ip string) (user.User, string, error) {
	// Asked before the limiter reserves anything, in the order Login asks.
	// With no token: a console connection has no browser to carry one, so a
	// guard that needs one refuses here as it refuses a provider sign-up.
	if _, err := s.runGuards(ctx, GuardLogin, nil, ip, identifier); err != nil {
		return user.User{}, "", err
	}

	attempt, err := s.limiter.Begin(ip, identifier)
	if err != nil {
		return user.User{}, "", err
	}
	defer attempt.finish(attemptCancelled)

	account, hash, err := s.users.CredentialsByLogin(ctx, identifier)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			s.hasher.DummyVerify(ctx, password)
			attempt.finish(attemptFailedUnknown)
			return user.User{}, "", ErrInvalidCredentials
		}
		return user.User{}, "", err
	}
	if s.afterCredentialRead != nil {
		s.afterCredentialRead()
	}

	ok, needsRehash, err := s.hasher.Verify(ctx, hash, password)
	if errors.Is(err, ErrInvalidHash) {
		// No password on this account at all — see Login, which answers the
		// same way for the same reason.
		s.hasher.DummyVerify(ctx, password)
		attempt.finish(attemptFailed)
		return user.User{}, "", ErrInvalidCredentials
	}
	if err != nil {
		return user.User{}, "", err
	}
	if !ok {
		attempt.finish(attemptFailed)
		return user.User{}, "", ErrInvalidCredentials
	}
	if !account.IsActive() {
		return user.User{}, "", &AccountDisabledError{Reason: account.BanReason}
	}

	attempt.finish(attemptSucceeded)

	// The rehash moves the stored hash, so the fingerprint returned below stops
	// matching CredentialFingerprint, and the console connection this sign-in
	// opens ends at its first command. That happens once per account per
	// parameter change. Re-reading the hash after the upgrade would be the second
	// read this function exists to avoid, so the cost is accepted.
	if needsRehash {
		s.rehash(ctx, account.ID, hash, password)
	}
	return account, fingerprintOf(hash), nil
}

// rehash stores a hash made with the current parameters in place of the one a
// sign-in just verified, when that one was weaker. Only that hash is replaced:
// the new one is computed after the check, so a password changed in the meantime
// is already stored, and it has to stay. A failed upgrade is not a failed
// sign-in, and the next sign-in tries again.
func (s *Service) rehash(ctx context.Context, userID, verified, password string) {
	upgraded, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return
	}
	if s.beforeRehashWrite != nil {
		s.beforeRehashWrite()
	}
	_, _ = s.users.ReplacePasswordHash(ctx, nil, userID, verified, upgraded)
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.sessions.DeleteByToken(ctx, token)
}

// ConfirmPassword is the guard in front of the changes a stolen session must
// not be able to make on its own: switching two-step sign-in on, moving the
// email address. It asks for the account's current password, and asks for
// nothing where the account has none — one opened through a provider has no
// password to prove, and demanding one would lock it out of its own settings.
func (s *Service) ConfirmPassword(ctx context.Context, userID, candidate string) error {
	hash, err := s.users.PasswordHash(ctx, nil, userID)
	if err != nil {
		return err
	}
	if hash == "" {
		return nil
	}
	if candidate == "" {
		return ErrPasswordRequired
	}
	return s.checkPassword(ctx, userID, hash, candidate)
}

// ConfirmNewSignIn is the proof in front of a new way into an account, which is
// what connecting a provider is. It asks what ChangePassword asks of the same
// account: the current password where there is one, and a sign-in made just now
// where there is none, the rule that lets an account opened through a provider
// set its first password. A bare session is neither, so a copied session cookie
// adds nothing without the password, and for an account with none, nothing once
// the sign-in it came from is older than recentSignIn.
func (s *Service) ConfirmNewSignIn(ctx context.Context, userID, candidate, sessionID string) error {
	hash, err := s.users.PasswordHash(ctx, nil, userID)
	if err != nil {
		return err
	}
	if hash != "" {
		if candidate == "" {
			return ErrPasswordRequired
		}
		return s.checkPassword(ctx, userID, hash, candidate)
	}
	return s.requireRecentSignIn(ctx, s.db, userID, sessionID)
}

// recentSignIn is how long after signing in a session may still set the first
// password on an account that has none. Long enough to go from the provider's
// consent screen to the settings page, short enough that a cookie copied from
// somewhere else is unlikely to still be young when it is used.
const recentSignIn = 15 * time.Minute

// requireRecentSignIn is the check that stands in for the current password on
// an account that has none. The session that asks has to have been created
// within recentSignIn. A session is judged by when it signed in, not when it
// was last used, and the sign-in time survives a password change because
// Reissue carries it over. A change with no session presented has no sign-in
// to judge, so it is refused rather than allowed.
func (s *Service) requireRecentSignIn(ctx context.Context, q database.Queryer, userID, sessionID string) error {
	if sessionID == "" {
		return ErrReauthRequired
	}
	var createdAt int64
	err := q.QueryRow(ctx,
		`SELECT created_at FROM sessions WHERE id = ? AND user_id = ? AND expires_at > ?`,
		sessionID, userID, time.Now().UnixMilli()).Scan(&createdAt)
	if err != nil {
		if database.IsNotFound(err) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("auth: read session sign-in time: %w", err)
	}
	if time.Since(time.UnixMilli(createdAt)) > recentSignIn {
		return ErrReauthRequired
	}
	return nil
}

// checkPassword verifies the current password behind a guessing budget.
// Without one, every endpoint that asks for the password would be a password
// oracle for whoever holds a session. The budget is the one the second step's
// codes use, keyed by the account alone (a session has no sign-in form to
// spray accounts from) and under its own prefix, so guessing a password can
// neither spend nor clear the allowance for guessing a code.
func (s *Service) checkPassword(ctx context.Context, userID, hash, candidate string) error {
	attempt, err := s.codes.Begin("", "password:"+userID)
	if err != nil {
		return err
	}
	defer attempt.finish(attemptCancelled)

	ok, _, err := s.hasher.Verify(ctx, hash, candidate)
	if err != nil {
		return err
	}
	if !ok {
		attempt.finish(attemptFailed)
		return ErrCurrentPasswordWrong
	}
	// Releases the reservation without forgiving the wrong guesses before it. The
	// owner's own right password is no reason to start a guesser's count over, the
	// rule the second step follows in spendCode.
	attempt.finish(attemptCancelled)
	return nil
}

// ChangePassword rotates a credential and ends every session on the account
// except the one making the change, which is reissued under a new token in the
// same transaction. The token it held is the one a thief would have copied, so
// leaving that row alive would leave the thief signed in.
//
// The returned token is the session's new cookie value. It is empty when
// sessionID is empty, which ends every session and issues nothing.
func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword, sessionID string) (string, error) {
	if err := ValidatePassword(newPassword); err != nil {
		return "", err
	}

	account, err := s.users.ByID(ctx, nil, userID)
	if err != nil {
		return "", err
	}
	_, hash, err := s.users.CredentialsByLogin(ctx, account.Username)
	if err != nil {
		return "", err
	}

	// An account opened through a provider has no password to confirm, and
	// this is where it gets its first one. Nothing is being replaced, so the
	// proof is the session instead, and it must be a sign-in made just now: see
	// requireRecentSignIn. Without this the only way into such an account is the
	// provider, and an operator switching that provider off would lock its owner
	// out.
	if hash != "" {
		if err := s.checkPassword(ctx, userID, hash, currentPassword); err != nil {
			return "", err
		}
		if same, _, _ := s.hasher.Verify(ctx, hash, newPassword); same {
			return "", ErrPasswordUnchanged
		}
	}

	updated, err := s.hasher.Hash(ctx, newPassword)
	if err != nil {
		return "", err
	}

	var token string
	err = s.db.Tx(ctx, func(tx *database.Tx) error {
		// Two changes from one session can both pass the password check above;
		// this transaction is what lets only one of them land. The account's own
		// row is taken first, so on Postgres the second change waits there until
		// the first commits, and then finds its session already reissued and
		// gone. SQLite runs write transactions one at a time, which has the same
		// effect.
		if _, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
			return err
		}
		if hash == "" {
			// The first password is set on the strength of the session alone, so
			// the check and the write below must not be split by another change.
			// Read under the lock: a password set by a change that committed after
			// the read above means this one was never allowed to skip the current
			// password, and the screen asks for it.
			current, err := s.users.PasswordHash(ctx, tx, userID)
			if err != nil {
				return err
			}
			if current != "" {
				return ErrCurrentPasswordWrong
			}
			if err := s.requireRecentSignIn(ctx, tx, userID, sessionID); err != nil {
				return err
			}
		}
		if err := s.users.SetPasswordHash(ctx, tx, userID, updated); err != nil {
			return err
		}
		if s.RevokeIssued != nil {
			if err := s.RevokeIssued(ctx, tx, userID); err != nil {
				return err
			}
		}
		if sessionID == "" {
			return s.sessions.DeleteByUser(ctx, tx, userID)
		}
		issued, err := s.sessions.Reissue(ctx, tx, userID, sessionID)
		if err != nil {
			return err
		}
		// The new row is excluded and the old one is not: it is what the new
		// one was copied from, and its token is the one that must stop working.
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`,
			userID, HashToken(issued)); err != nil {
			return fmt.Errorf("auth: revoke other sessions: %w", err)
		}
		token = issued
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// SetPassword is the administrator's reset: no current password, and every
// session for that account is dropped.
func (s *Service) SetPassword(ctx context.Context, userID, newPassword string, authorize ...func(database.Queryer, user.User) error) error {
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, newPassword)
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		if len(authorize) > 0 {
			// Role changes use this same lock, so a password reset cannot race
			// a promotion and silently take over a newly privileged account.
			if err := settings.Lock(ctx, tx); err != nil {
				return err
			}
			target, err := s.users.ByID(ctx, tx, userID)
			if err != nil {
				return err
			}
			if err := authorize[0](tx, target); err != nil {
				return err
			}
		}
		if err := s.users.SetPasswordHash(ctx, tx, userID, hash); err != nil {
			return err
		}
		if s.RevokeIssued != nil {
			if err := s.RevokeIssued(ctx, tx, userID); err != nil {
				return err
			}
		}
		return s.sessions.DeleteByUser(ctx, tx, userID)
	})
}

// Authenticate resolves a cookie to its account. A disabled account is
// rejected here, so a session issued before the account was disabled stops
// working on its next request rather than at its next expiry.
//
// A session still waiting for its second step is refused with
// ErrSignInIncomplete: it proves a password, and a password is not what an
// account with two-step sign-in agreed to be enough.
func (s *Service) Authenticate(ctx context.Context, token string) (user.User, Session, error) {
	session, account, err := s.sessions.GetWithUser(ctx, token)
	if err != nil {
		return user.User{}, Session{}, err
	}
	if !account.IsActive() {
		return user.User{}, Session{}, &AccountDisabledError{Reason: account.BanReason}
	}
	if session.TwoFactorPending {
		return user.User{}, Session{}, ErrSignInIncomplete
	}
	if time.Since(time.UnixMilli(account.LastActiveAt)) >= time.Minute {
		now := time.Now().UnixMilli()
		// Bookkeeping, not the answer. A write that fails — a SQLite writer
		// holding the lock past busy_timeout, a Postgres blip — used to fail
		// the whole lookup, and Attach then served a signed-in reader as
		// anonymous: a 401 for their chat because a timestamp did not land.
		// The next request a minute on tries again.
		if err := s.users.MarkActive(ctx, account.ID, now); err != nil {
			slog.WarnContext(ctx, "could not record account activity", "user", account.ID, "error", err)
		} else {
			account.LastActiveAt = now
		}
	}

	if time.Since(time.UnixMilli(session.LastSeenAt)) > s.cfg.TouchInterval {
		_ = s.sessions.Touch(ctx, session.ID, s.cfg.TTL)
	}
	return account, session, nil
}

// Resolve answers what Authenticate answers, which account this cookie is and
// whether it may still act. A background stream asks it before each run after
// the first. The person behind a stream is not necessarily there, so Resolve
// neither renews the sign-in nor stamps the account as active.
//
// Resolve is not write-free. The lookup it shares with Authenticate,
// GetWithUser, removes a session row that has already expired (a failed delete
// is ignored), and it reverts a group membership whose grant has lapsed to the
// default group, writing group_id, group_expires_at and updated_at on the user
// row. Each only records an end that time has already reached; neither extends
// a sign-in or a grant.
func (s *Service) Resolve(ctx context.Context, token string) (user.User, error) {
	session, account, err := s.sessions.GetWithUser(ctx, token)
	if err != nil {
		return user.User{}, err
	}
	if !account.IsActive() {
		return user.User{}, &AccountDisabledError{Reason: account.BanReason}
	}
	if session.TwoFactorPending {
		return user.User{}, ErrSignInIncomplete
	}
	return account, nil
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

// countRecentFromIP is the one piece of context the reviewer cannot see in
// the request: how many accounts this address has already made. The window is
// the operator's per-address one, or an hour where they have not set one —
// the number is context for a judgement, not a limit being enforced.
func (s *Service) countRecentFromIP(ctx context.Context, ip string) (int, error) {
	minutes := s.settings.Int(settings.SignupsIPWindowMin, 60)
	if minutes <= 0 {
		minutes = 60
	}
	since := time.Now().Add(-time.Duration(minutes) * time.Minute).UnixMilli()
	return s.users.CountFromIP(ctx, nil, ip, since)
}

func (s *Service) isThirdPartyOnlySignup() bool {
	return s.settings.Bool(settings.OAuthThirdPartyOnlySignup) || s.settings.Bool(settings.OAuthOIDCOnlySignup)
}

func (s *Service) hasOAuthProviders() bool {
	return s.settings.Bool(settings.OAuthGitHubEnabled) ||
		s.settings.Bool(settings.OAuthGoogleEnabled) ||
		s.settings.Bool(settings.OAuthOIDCEnabled)
}
