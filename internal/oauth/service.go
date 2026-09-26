package oauth

import (
	"context"
	"errors"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Turning "GitHub says this is user 4218" into an account of this instance's.
//
// Three outcomes, in this order: the identity is already connected and its
// account signs in; the provider has proved an address this instance already
// holds, and the two are the same person; or nobody here is that person yet
// and an account is opened. The order is the whole design — the subject is
// the identity, and the address is only ever a way to recognise an account
// that predates the connection.

var (
	// The address is spoken for and this sign-in may not adopt it: either the
	// operator turned that off, or the account already answers to a different
	// account at the same provider. Both have the same remedy, which is to
	// sign in the usual way and connect from the settings screen.
	ErrAddressTaken = errors.New("oauth: an account here already uses that address")
	// A provider identity nobody knows, on an instance that does not open
	// accounts this way.
	ErrSignupClosed = errors.New("oauth: this server does not open accounts from a provider sign-in")
	// Removing this connection would leave no way into the account.
	ErrLastWayIn = errors.New("oauth: this is the only way left into this account")
	// There was nothing to remove.
	ErrNotConnected        = errors.New("oauth: that provider is not connected to this account")
	errNeedsEmailScreening = errors.New("oauth: email screening must run before opening this account")
)

// MoreDetailsNeeded says the sign-in stopped one step short.
//
// The instance requires something no provider has to give — a QQ number, or an
// address where the provider proved none — so the person has to be asked
// before an account can be opened for them. Nothing has been written when this
// is returned: the answer is a form, not an apology, and the account is opened
// by Complete once it comes back.
type MoreDetailsNeeded struct {
	Identity Identity
	Missing  auth.Missing
}

func (e *MoreDetailsNeeded) Error() string {
	return "oauth: this sign-in needs details the provider could not supply"
}

// Details are those answers.
type Details struct {
	QQ    string
	Email string
	// Empty unless MissingFor asked for one (an invite-only instance) and
	// the completion form was shown it.
	Invite string
}

// credentials names the three settings each provider is configured with.
//
// A map rather than a key built out of the provider id, so that the strings
// here are the same constants the settings screen and the writable list use,
// and TestEveryProviderHasItsSettings can tell when a provider is added
// without them.
var credentials = map[string][3]string{
	"github": {settings.OAuthGitHubEnabled, settings.OAuthGitHubID, settings.OAuthGitHubSecret},
	"google": {settings.OAuthGoogleEnabled, settings.OAuthGoogleID, settings.OAuthGoogleSecret},
}

type Service struct {
	db       *database.DB
	store    *Store
	users    *user.Store
	auth     *auth.Service
	settings *settings.Service
}

func NewService(
	db *database.DB, store *Store, users *user.Store, authService *auth.Service, set *settings.Service,
) *Service {
	return &Service{db: db, store: store, users: users, auth: authService, settings: set}
}

// Enabled reports whether the operator has both switched this provider on and
// finished configuring it. A button drawn for a provider with no client id is
// a button that leads to an apology.
func (s *Service) Enabled(providerID string) bool {
	keys, known := credentials[providerID]
	if !known {
		return false
	}
	return s.settings.Bool(keys[0]) && s.Credentials(providerID).configured()
}

func (s *Service) Credentials(providerID string) Credentials {
	keys, known := credentials[providerID]
	if !known {
		return Credentials{}
	}
	return Credentials{
		ClientID:     strings.TrimSpace(s.settings.Get(keys[1])),
		ClientSecret: strings.TrimSpace(s.settings.Get(keys[2])),
	}
}

// SignIn resolves a provider's answer to an account, opening one if this
// instance allows it.
//
// It returns MoreDetailsNeeded rather than opening an account that would break
// one of this instance's registration rules. See resolve.
func (s *Service) SignIn(ctx context.Context, identity Identity, ip, ua string) (user.User, error) {
	return s.resolve(ctx, identity, Details{}, true, ip, ua)
}

// Complete opens the account SignIn stopped short of, with the answers the
// person has since given.
//
// The same resolution runs again from the top rather than picking up where it
// left off: minutes have passed while a form was being filled in, and in that
// time the identity may have been connected in another tab, the instance may
// have closed registration, or somebody else may have taken the address. What
// was true when the question was asked is not what decides.
func (s *Service) Complete(
	ctx context.Context, identity Identity, details Details, ip, ua string,
) (user.User, error) {
	return s.resolve(ctx, identity, details, false, ip, ua)
}

// resolve is both of the above. `ask` is what separates them: the first pass
// may stop and ask, the second has the answers and must either open the
// account or be refused.
func (s *Service) resolve(
	ctx context.Context, identity Identity, details Details, ask bool, ip, ua string,
) (user.User, error) {
	// The one address Provision could store. A provider's proven address wins;
	// otherwise the completion form supplied it. The provider call below must
	// stay outside the transaction, so cheap reads first keep linked identities
	// and addresses already held here from spending paid lookup credits.
	email := strings.TrimSpace(identity.Email)
	if email == "" {
		email = strings.TrimSpace(details.Email)
	}
	screened := false
	var screeningErr error
	if email != "" {
		found, err := s.store.Account(ctx, nil, identity.Provider, identity.Subject)
		if err != nil && !errors.Is(err, ErrNoIdentity) {
			return user.User{}, err
		}
		if errors.Is(err, ErrNoIdentity) {
			_, err := s.users.ByEmail(ctx, nil, email)
			switch {
			case err == nil:
				// The provider may connect to an address the instance already
				// holds; that is an existing account, not a new registration.
			case errors.Is(err, user.ErrNotFound):
				shouldOpen := true
				// A first pass that will only return the details form does not
				// create an account. The completion request screens the final
				// address instead.
				if ask {
					missing, missingErr := s.auth.MissingFor(ctx, nil, identity.Email)
					if missingErr != nil {
						return user.User{}, missingErr
					}
					shouldOpen = !missing.Any()
				}
				if shouldOpen {
					populated, err := s.users.Any(ctx, nil)
					if err != nil {
						return user.User{}, err
					}
					shouldOpen = !populated || s.settings.Bool(settings.OAuthAllowSignup)
					if populated && shouldOpen {
						screeningErr = s.auth.CheckRegistrationEmail(ctx, email)
						screened = true
						if ctx.Err() != nil {
							return user.User{}, ctx.Err()
						}
					}
				}
			default:
				return user.User{}, err
			}
		} else if found == "" {
			return user.User{}, ErrNoIdentity
		}
	}

	var account user.User
	for {
		account = user.User{}
		err := s.db.Tx(ctx, func(tx *database.Tx) error {
			// The common path, and the cheap one: somebody signing in again.
			found, err := s.store.Account(ctx, tx, identity.Provider, identity.Subject)
			if err == nil {
				account, err = s.users.ByID(ctx, tx, found)
				if err != nil {
					return err
				}
				if !account.IsActive() {
					return auth.ErrAccountDisabled
				}
				return s.store.Touch(ctx, tx, identity)
			}
			if !errors.Is(err, ErrNoIdentity) {
				return err
			}

			// From here the transaction may create an account, so it takes the
			// instance lock — the one Register and Provision take — before
			// deciding anything. Provision takes it again and that is free; what
			// matters is that the lookup below and the write after it cannot be
			// split by another sign-in doing the same thing.
			if err := settings.Lock(ctx, tx); err != nil {
				return err
			}
			// Read again under the lock: the request that was ahead of this one
			// may have just connected this very identity.
			if found, err := s.store.Account(ctx, tx, identity.Provider, identity.Subject); err == nil {
				account, err = s.users.ByID(ctx, tx, found)
				if err != nil {
					return err
				}
				if !account.IsActive() {
					return auth.ErrAccountDisabled
				}
				return s.store.Touch(ctx, tx, identity)
			} else if !errors.Is(err, ErrNoIdentity) {
				return err
			}

			// An address the provider has proved, on an account that already
			// exists here: the same person, arriving a different way.
			if identity.Email != "" {
				existing, err := s.users.ByEmail(ctx, tx, identity.Email)
				switch {
				case err == nil:
					if !s.settings.Bool(settings.OAuthLinkByEmail) {
						return ErrAddressTaken
					}
					if !existing.IsActive() {
						return auth.ErrAccountDisabled
					}
					if err := s.store.Link(ctx, tx, existing.ID, identity); err != nil {
						// The account already answers to a different account at
						// this provider. Said as "taken" rather than as a
						// conflict, because from outside that is what it is.
						if errors.Is(err, ErrAlreadyLinked) {
							return ErrAddressTaken
						}
						return err
					}
					account = existing
					return nil
				case !errors.Is(err, user.ErrNotFound):
					return err
				}
			}

			// Nobody here is this person yet.
			if !s.settings.Bool(settings.OAuthAllowSignup) {
				// Unless there is nobody here at all. An empty instance is being
				// set up, and refusing the first account would leave a deployment
				// with no way in but the setting nobody can reach to change.
				populated, err := s.users.Any(ctx, tx)
				if err != nil {
					return err
				}
				if populated {
					return ErrSignupClosed
				}
			}

			// What this instance requires that the provider could not supply. On
			// the first pass that is a question to go and ask; on the second the
			// answers are in hand and Provision checks them itself.
			if ask {
				missing, err := s.auth.MissingFor(ctx, tx, identity.Email)
				if err != nil {
					return err
				}
				if missing.Any() {
					return &MoreDetailsNeeded{Identity: identity, Missing: missing}
				}
			}

			// The provider's address when it proved one, and the typed one
			// otherwise. Which of the two it is decides whether the account
			// arrives confirmed: a provider's verified address is as good as a
			// link this server posted, and a typed one is not.
			address := identity.Email
			if address == "" {
				address = strings.TrimSpace(details.Email)
			}
			populated, err := s.users.Any(ctx, tx)
			if err != nil {
				return err
			}
			if populated && email != "" {
				if !screened {
					// The preflight saw an empty instance or a detail form. Another
					// account won the race before this transaction took the settings
					// row lock, so roll back, screen outside the transaction, then
					// repeat the authoritative identity/address checks.
					return errNeedsEmailScreening
				}
				if screeningErr != nil {
					return screeningErr
				}
			}

			created, err := s.auth.Provision(ctx, tx, auth.ProvisionInput{
				Username:      identity.Login,
				Email:         address,
				EmailVerified: identity.Email != "",
				QQ:            strings.TrimSpace(details.QQ),
				Nickname:      strings.TrimSpace(identity.Name),
				IP:            ip,
				UA:            ua,
				InviteCode:    strings.TrimSpace(details.Invite),
			})
			if err != nil {
				return err
			}
			if err := s.store.Link(ctx, tx, created.ID, identity); err != nil {
				return err
			}
			account = created
			return nil
		})
		if errors.Is(err, errNeedsEmailScreening) {
			screeningErr = s.auth.CheckRegistrationEmail(ctx, email)
			screened = true
			if ctx.Err() != nil {
				return user.User{}, ctx.Err()
			}
			continue
		}
		if err != nil {
			return user.User{}, err
		}
		break
	}
	// Provision could not call this itself: it ran inside the transaction
	// above, and the invite use it may have recorded was not visible outside
	// that transaction until the commit this line is now past. A no-op for
	// every path through resolve that did not just provision a fresh account
	// through a personal code — see auth.Service.RewardInvite.
	if s.auth.RewardInvite != nil {
		s.auth.RewardInvite(ctx, account.ID, s.auth.VerificationRequired())
	}

	// An address somebody typed is unconfirmed, and this is the instance that
	// asked for it — so the link goes out the way it does for the sign-up
	// form. Best effort: the account exists either way, and there is a resend
	// button behind the banner.
	if !account.EmailVerified && account.Email != "" {
		_ = s.auth.Resend(ctx, s.settings.Get(settings.SiteName), account.ID)
	}
	return account, nil
}

// The two things the completion form says about an address, for the same
// reason the sign-up form says them: which addresses would be accepted, and
// whether a link is coming. Read from the settings here rather than by the
// handler, so there is one place that knows where they live.
func (s *Service) emailDomains() []string {
	return auth.ParseDomains(s.settings.Get(settings.EmailDomains))
}

func (s *Service) verificationRequired() bool { return s.auth.VerificationRequired() }

// Connect adds a provider to an account that is already signed in.
func (s *Service) Connect(ctx context.Context, userID string, identity Identity) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		// The account's own row, because what follows is a check — is this
		// identity spoken for — and then a write against the same account.
		if _, err := tx.Exec(ctx,
			`UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
			return err
		}
		switch existing, err := s.store.Account(ctx, tx, identity.Provider, identity.Subject); {
		case err == nil:
			if existing == userID {
				// Already connected, to this very account. Refreshing what
				// the provider says is the whole of the work.
				return s.store.Touch(ctx, tx, identity)
			}
			return ErrAlreadyLinked
		case !errors.Is(err, ErrNoIdentity):
			return err
		}
		return s.store.Link(ctx, tx, userID, identity)
	})
}

// Connections is what an account's settings screen shows, with the one fact
// the screen needs beside them: whether there is also a password, because
// that is what decides whether a connection may be removed.
func (s *Service) Connections(ctx context.Context, userID string) ([]Connection, bool, error) {
	items, err := s.store.For(ctx, nil, userID)
	if err != nil {
		return nil, false, err
	}
	hash, err := s.users.PasswordHash(ctx, nil, userID)
	if err != nil {
		return nil, false, err
	}
	return items, hash != "", nil
}

// Disconnect removes a connection, unless it is the last way in.
//
// An account with no password whose only connection is removed is an account
// nobody can reach — not disabled, not deleted, simply unreachable, with its
// conversations still in it. The check and the delete hold the account's row
// lock, because two clicks on two providers would otherwise each see the
// other still there and both go through.
func (s *Service) Disconnect(ctx context.Context, userID, provider string) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
			return err
		}
		hash, err := s.users.PasswordHash(ctx, tx, userID)
		if err != nil {
			return err
		}
		count, err := s.store.Count(ctx, tx, userID)
		if err != nil {
			return err
		}
		if hash == "" && count <= 1 {
			return ErrLastWayIn
		}
		removed, err := s.store.Unlink(ctx, tx, userID, provider)
		if err != nil {
			return err
		}
		if !removed {
			return ErrNotConnected
		}
		return nil
	})
}
