package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Accounts opened by something other than the sign-up form.
//
// Today that is a provider sign-in: GitHub or Google has already established
// who is at the browser, and what is left is the part this instance owns —
// whether it is accepting accounts at all, which addresses it accepts, how
// fast they may appear, which group they land in, and whether this is the
// first account and therefore the administrator.
//
// That list lives here rather than in internal/oauth on purpose. It is the
// same list Register applies, and two copies of it would drift the first time
// an operator asked for a rule the sign-up form has and the provider button
// does not — which is exactly the shape of bug the sign-up controls exist to
// prevent.

// ErrNoUsernameAvailable means every candidate derived from the provider's
// name was taken. Vanishingly unlikely, and better than a loop that never
// ends.
var ErrNoUsernameAvailable = errors.New("auth: no username could be derived for this account")

// ProvisionInput is an account described by a provider rather than by a form.
type ProvisionInput struct {
	// A suggestion. A taken one is suffixed rather than refused: nobody
	// pressing "continue with GitHub" chose a username here, so there is
	// nobody to tell that theirs is unavailable.
	Username string
	// The address, from whichever of the two places it came: proved by the
	// provider, or typed by the person when this instance asked for one the
	// provider could not supply.
	Email string
	// Whether that address arrived proved. A provider's verified address is
	// as good as a link this server posted itself; one somebody typed into
	// the completion form is exactly as unproven as one typed into the
	// sign-up form, and is held back the same way.
	EmailVerified bool
	// Likewise: a provider has no QQ number to offer, so this is empty unless
	// the person was asked for one.
	QQ       string
	Nickname string
	IP       string
	UA       string
	// Empty unless the person was asked and answered — see Missing.Invite.
	InviteCode string
}

// Missing is what an instance requires that a provider cannot answer.
//
// It exists so the caller can ask before it starts rather than discover it by
// being refused: a sign-in that needs a QQ number should end at a form asking
// for one, not at an apology.
type Missing struct {
	QQ     bool
	Email  bool
	Invite bool
}

func (m Missing) Any() bool { return m.QQ || m.Email || m.Invite }

// MissingFor reports what has to be asked of somebody arriving with this
// identity before an account can be opened for them.
//
// The first account is exempt, for the same reason it is exempt from every
// other registration control: it is the one that turns an empty instance into
// an administered one, and there is nobody yet to have configured these rules.
func (s *Service) MissingFor(ctx context.Context, q database.Queryer, email string) (Missing, error) {
	populated, err := s.users.Any(ctx, q)
	if err != nil {
		return Missing{}, err
	}
	if !populated {
		return Missing{}, nil
	}
	return Missing{
		QQ:     s.settings.Get(settings.QQRequirement) == settings.QQRequired,
		Email:  strings.TrimSpace(email) == "" && (s.settings.Bool(settings.RequireEmail) || s.VerificationRequired()),
		Invite: s.settings.Bool(settings.InvitesRequired),
	}, nil
}

// Provision creates the account.
//
// It runs inside the caller's transaction and takes the instance lock itself,
// so a provider sign-in and a form registration cannot both decide they are
// the first account, and the per-address count cannot be read by two of them
// before either has written.
//
// Deliberately not run past the sign-up reviewer. That review reads a
// username, an address and a user agent and judges whether a person chose
// them; here nobody chose them, and the provider has already done the work of
// establishing that an account exists somewhere with a history. Sending it a
// row it cannot judge would produce a verdict about nothing.
func (s *Service) Provision(ctx context.Context, tx *database.Tx, in ProvisionInput) (user.User, error) {
	if err := settings.Lock(ctx, tx); err != nil {
		return user.User{}, err
	}

	total, err := s.users.Count(ctx, tx)
	if err != nil {
		return user.User{}, err
	}
	first := total == 0

	// None of the registration controls apply to the first account, for the
	// reason Register gives: it is the one that turns an empty instance into
	// an administered one.
	if !first {
		if !s.settings.Bool(settings.RegistrationEnabled) {
			return user.User{}, ErrRegistrationClosed
		}
		if err := checkEmail(s.settings, in.Email); err != nil {
			return user.User{}, err
		}
		if s.VerificationRequired() && strings.TrimSpace(in.Email) == "" {
			return user.User{}, ErrEmailRequired
		}
		// The same check the sign-up form makes. A provider has none of this
		// to offer, so by the time a caller reaches here it has either asked
		// the person or it is about to be refused — and being refused is the
		// right answer for a caller that did not ask.
		if err := checkQQ(s.settings, in.QQ); err != nil {
			return user.User{}, err
		}
		if allowed, retryAfter := s.signups.allow(
			s.settings.Int(settings.SignupsPerMinute, 0),
			s.settings.Int(settings.SignupsPerHour, 0),
		); !allowed {
			return user.User{}, &SignupThrottleError{RetryAfter: retryAfter}
		}
		if err := s.checkSignupIP(ctx, tx, in.IP); err != nil {
			return user.User{}, err
		}
	}

	// The same spend Register makes, on the same terms: inside this
	// transaction, so a failure below — a taken address, the throttle —
	// hands the use back by rolling back with everything else.
	var grant *InviteGrant
	if !first {
		grant, err = s.consumeInvite(ctx, tx, in.InviteCode)
		if err != nil {
			return user.User{}, err
		}
	}

	username, err := s.availableUsername(ctx, tx, in.Username)
	if err != nil {
		return user.User{}, err
	}
	// The address and the number are the two things here somebody else may
	// already hold. For an address the caller looks first and links instead
	// where it does, so reaching this with a taken one means the two accounts
	// are not the same person; a number is simply taken.
	if strings.TrimSpace(in.Email) != "" || strings.TrimSpace(in.QQ) != "" {
		_, emailTaken, qqTaken, err := s.users.Exists(ctx, tx, username, in.Email, in.QQ)
		if err != nil {
			return user.User{}, err
		}
		if emailTaken {
			return user.User{}, user.ErrEmailTaken
		}
		if qqTaken {
			return user.User{}, user.ErrQQTaken
		}
	}

	groupID, err := s.registrationGroup(ctx, tx)
	if err != nil {
		return user.User{}, err
	}
	if grant != nil && grant.GroupID != "" {
		groupID = grant.GroupID
	}
	role := user.RoleUser
	if first {
		role = user.RoleSuperAdmin
	}

	created, err := s.users.Create(ctx, tx, user.CreateInput{
		Username: username,
		Email:    in.Email,
		QQ:       in.QQ,
		Nickname: in.Nickname,
		// No password. Not a placeholder and not a random one nobody knows: a
		// credential that exists is a credential that can be guessed at, and
		// this account has never had one. Login answers an attempt against it
		// exactly as it answers a wrong password; the owner can set one from
		// their own settings, which is the only place that knows it is them.
		PasswordHash: "",
		Role:         role,
		GroupID:      groupID,
		Status:       user.StatusActive,
		// An address the provider proved needs no link. One somebody typed
		// into the completion form is held back exactly as the sign-up form
		// holds one back, because it is exactly as unproven.
		Unverified:      !first && !in.EmailVerified && s.VerificationRequired(),
		SignupIP:        in.IP,
		SignupUserAgent: in.UA,
	})
	if err != nil {
		return user.User{}, err
	}
	if _, err := s.applyInvite(ctx, tx, grant, created.ID); err != nil {
		return user.User{}, err
	}

	// Inside the lock, for the reason Register gives: recording after commit
	// leaves a gap in which the next queued sign-up passes a throttle this
	// one should already have moved.
	s.signups.record()
	// RewardInvite is deliberately not called here: this method runs inside
	// tx, a transaction it was handed rather than one it owns, and the use
	// applyInvite just recorded through it is not visible outside tx until
	// the caller commits. oauth.Service calls RewardInvite itself, once its
	// own s.db.Tx has returned.
	return created, nil
}

// StartSession issues a session for an account something else has already
// authenticated.
//
// It exists so that session policy — the lifetime, the address and client
// recorded against it, the login timestamp, and the second step — stays in
// one place instead of being reimplemented by every caller that can establish
// who somebody is. A provider vouching for somebody is a first factor like a
// password is, so an account with two-step sign-in gets *SecondFactorRequired
// here exactly as it does from Login.
func (s *Service) StartSession(ctx context.Context, account user.User, ip, ua, remembered string) (string, error) {
	return s.secondStep(ctx, account, remembered, ip, ua)
}

// CheckEmail applies the instance's address rules to an address that did not
// come from the sign-up form. Exported for the same reason ParseDomains is:
// the rule is the operator's, and it has to hold on every way in.
func CheckEmail(set *settings.Service, email string) error { return checkEmail(set, email) }

// availableUsername turns a provider's name for somebody into one this
// instance can store, then finds a spelling nobody has taken.
func (s *Service) availableUsername(ctx context.Context, q database.Queryer, suggestion string) (string, error) {
	base := sanitiseUsername(suggestion)

	candidates := make([]string, 0, 15)
	candidates = append(candidates, base)
	for n := 2; n <= 9; n++ {
		candidates = append(candidates, truncateUsername(base, 31)+strconv.Itoa(n))
	}
	// Then four random digits. Counting further would walk an attacker
	// straight to "who already has this name"; a random tail just works.
	for attempt := 0; attempt < 5; attempt++ {
		candidates = append(candidates, truncateUsername(base, 28)+randomDigits(4))
	}

	for _, candidate := range candidates {
		if err := user.ValidateUsername(candidate); err != nil {
			continue
		}
		taken, _, _, err := s.users.Exists(ctx, q, candidate, "", "")
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", ErrNoUsernameAvailable
}

// sanitiseUsername keeps what user.ValidateUsername accepts and drops the
// rest. A name written in a script this column does not hold — which is most
// of them — comes out empty and gets the generic base, because a username
// nobody chose only has to be unique and readable.
func sanitiseUsername(raw string) string {
	var out strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			out.WriteRune(r)
		}
	}
	// Leading and trailing punctuation is accepted by the pattern and reads
	// as a mistake on a profile.
	cleaned := strings.Trim(out.String(), "._-")
	if len(cleaned) < 3 {
		return "user"
	}
	return truncateUsername(cleaned, 32)
}

func truncateUsername(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.Trim(value[:limit], "._-")
}

func randomDigits(count int) string {
	var out strings.Builder
	for i := 0; i < count; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			// Same reasoning as the state nonce: there is no sensible
			// fallback for missing randomness, and carrying on with a
			// predictable one is worse than stopping.
			panic(fmt.Sprintf("auth: no randomness available: %v", err))
		}
		out.WriteString(n.String())
	}
	return out.String()
}
