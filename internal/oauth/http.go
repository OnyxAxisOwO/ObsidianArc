package oauth

import (
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/pow"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usercheck"
)

// The two halves of a sign-in are ordinary navigations, not API calls: the
// browser leaves for the provider and comes back, and both ends of that trip
// have to be something a person can land on. So these two answer with a
// redirect even when they fail — a page that says what went wrong in the
// reader's own language, rather than a JSON error rendered raw in a tab.
//
// The other two are API calls like any other, and answer like them.

// statePath scopes the state cookie. Every route below sits under it.
const statePath = "/api/auth/oauth"

type Handlers struct {
	service *Service
	stamp   *stamp
	// A second signature for the half-finished sign-ups, derived separately
	// so one kind of ticket can never be presented as the other.
	pending *stamp

	// Whether the state cookie is marked Secure. Follows the session
	// cookie's own setting, so a plain-HTTP development instance still works
	// and a real one never sends it in the clear.
	SecureCookie bool
	// The address this instance answers at, resolved by the wiring through
	// httpx.PublicOrigin so that every URL this server hands to somebody else
	// agrees. Nil falls back to the request's own host and TLS state, which
	// is wrong behind a proxy and right in a test.
	Origin func(*http.Request) string
	// Who is calling, for the per-address registration limit.
	ClientIP func(*http.Request) string
	// One client for every provider call, so a token exchange reuses the
	// connection the identity lookup just opened.
	Client *http.Client

	Challenge       turnstile.Gate
	SignupChallenge turnstile.Gate
	LoginChallenge  turnstile.Gate
}

func NewHandlers(service *Service, secret []byte) *Handlers {
	return &Handlers{
		service: service,
		stamp:   newStamp(secret),
		pending: newPendingStamp(secret),
	}
}

func (h *Handlers) Routes(mux *http.ServeMux) {
	// Public, and necessarily so: nobody signing in has a session yet, and
	// the provider sends the browser back with nothing but the code.
	mux.HandleFunc("GET /api/auth/oauth/start/{provider}", h.start)
	mux.HandleFunc("GET /api/auth/oauth/callback/{provider}", h.callback)

	protected := func(handler httpx.Handler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			auth.RequireUser(httpx.Wrap(handler)).ServeHTTP(w, r)
		}
	}
	// Public, and necessarily so: the person filling this in has no account
	// yet — that is the whole point of the form. What stands in for a session
	// is the signed cookie the callback left behind.
	mux.HandleFunc("GET /api/auth/oauth/signup", httpx.Wrap(h.pendingSignup))
	mux.HandleFunc("POST /api/auth/oauth/signup", httpx.Wrap(h.completeSignup))
	mux.HandleFunc("GET /api/auth/oauth/connections", protected(h.connections))
	mux.HandleFunc("DELETE /api/auth/oauth/connections/{provider}", protected(h.disconnect))
}

// --- the browser's round trip -------------------------------------------------

// gateFor is the Turnstile gate in front of one of the two doors a sign-in can
// come through: the sign-up door when the browser asked for the register page's
// flow, the login door otherwise. A door with no gate of its own falls back to
// the general one. start and admission both ask it, so they agree on the
// Turnstile gate in front of sign-up.
//
// Proof of work is not a case in this function, and not because it cannot apply
// to a provider sign-up: the sign-up door checks it in start (see proofRequired).
// What decides where a check can run is where the browser's answer can travel.
// The provider's redirect back carries nothing the browser solved, so the
// callback has no answer to check. start is the browser's own navigation to this
// server, so it carries the proof of work in its query, as it carries the
// Turnstile token, and the pass it records travels in the signed state.
//
// So a provider sign-up is covered by the Turnstile gate and the proof of work
// at the sign-up door, and by the plugin guards the callback asks before it
// opens an account. The AI sign-up reviewer does not run for it (see
// docs/admin/sign-in.md).
func (h *Handlers) gateFor(signingUp bool) turnstile.Gate {
	gate := h.LoginChallenge
	if signingUp {
		gate = h.SignupChallenge
	}
	if gate.Enabled == nil && h.Challenge.Enabled != nil {
		gate = h.Challenge
	}
	return gate
}

// signUpChallenged is whether a sign-up through this server stands behind a
// challenge at all: the Turnstile gate for sign-up, or the proof of work the
// registration captcha mode asks for. While it does, a sign-in that opens an
// account has to have passed the sign-up door it started at (see admission).
func (h *Handlers) signUpChallenged() bool {
	gate := h.gateFor(true)
	return (gate.Enabled != nil && gate.Enabled()) || h.proofRequired()
}

// proofRequired is whether the sign-up door asks for a proof of work. It reads
// the captcha mode the way Register does, so both doors ask for the same thing.
func (h *Handlers) proofRequired() bool {
	mode := h.service.settings.RegistrationCaptchaMode()
	return mode == settings.CaptchaModePoW || mode == settings.CaptchaModeBoth
}

// admission is what the sign-up door says about a sign-in that may open an
// account. While the sign-up challenge is on, a sign-in gets past it only by
// having passed it at the door it started at, because the provider's redirect
// back carries no answer to it.
func (h *Handlers) admission(passedSignUp bool) Admission {
	return Admission{SignUpCleared: !h.signUpChallenged() || passedSignUp}
}

// proofFrom reads the proof-of-work solution a sign-up carried on its start
// request. A value that is not one is no solution, and the check refuses it the
// way it refuses a missing one.
func proofFrom(raw string) *pow.Solution {
	var solution pow.Solution
	if err := json.Unmarshal([]byte(raw), &solution); err != nil {
		return nil
	}
	return &solution
}

func (h *Handlers) start(w http.ResponseWriter, r *http.Request) {
	provider := ByID(r.PathValue("provider"))
	if provider == nil {
		httpx.WriteError(w, r, httpx.NotFound("No such sign-in provider."))
		return
	}

	// Where a failure lands, decided before anything can fail: a person
	// adding a connection goes back to the page they started it from, and a
	// person signing in belongs at the sign-in card.
	account, signedIn := auth.UserFrom(r.Context())
	linking := signedIn && r.URL.Query().Get("link") == "1"
	back := ""
	if linking {
		back = linkBack(r.URL.Query().Get("next"))
	}

	if !h.service.Enabled(provider.ID) {
		h.fail(w, r, back, "unavailable")
		return
	}

	// Whether this sign-in passed the sign-up challenge at this door. Only a
	// pass counts: a sign-up started while the challenge was off is held to it
	// if the operator turns it on before the provider comes back.
	passedSignUp := false
	if !linking {
		signingUp := r.URL.Query().Get("register") == "1"
		// Set only once a check has actually run here: a challenge that is off
		// at this moment passes nothing.
		checked := false
		gate := h.gateFor(signingUp)
		if gate.Enabled != nil && gate.Enabled() {
			if err := gate.Check(r.Context(), r.URL.Query().Get("turnstile"), h.address(r)); err != nil {
				h.fail(w, r, back, "challenge_failed")
				return
			}
			checked = true
		}
		if signingUp && h.proofRequired() {
			proof := proofFrom(r.URL.Query().Get("pow"))
			if err := h.service.auth.CheckSignupProof(r.Context(), h.address(r), proof); err != nil {
				h.fail(w, r, back, "challenge_failed")
				return
			}
			checked = true
		}
		passedSignUp = signingUp && checked
	}

	creds, err := h.service.ResolveCredentials(r.Context(), h.Client, provider.ID)
	if err != nil {
		h.fail(w, r, back, "unavailable")
		return
	}

	value := state{
		Provider: provider.ID,
		Nonce:    token(),
		Next:     safeNext(r.URL.Query().Get("next")),
		SignUp:   passedSignUp,
		Expiry:   time.Now().Add(stateTTL).UnixMilli(),
	}
	if linking {
		value.UserID = account.ID
	}
	challenge := ""
	if provider.PKCE {
		value.Verifier = token()
		challenge = challengeFor(value.Verifier)
	}

	cookie, err := h.stamp.issue(value)
	if err != nil {
		h.fail(w, r, back, "failed")
		return
	}
	h.setState(w, cookie)

	target := provider.authorise(creds,
		h.redirectURI(r, provider.ID), value.Nonce, challenge)
	if target == "" {
		h.fail(w, r, back, "unavailable")
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (h *Handlers) callback(w http.ResponseWriter, r *http.Request) {
	provider := ByID(r.PathValue("provider"))
	if provider == nil {
		httpx.WriteError(w, r, httpx.NotFound("No such sign-in provider."))
		return
	}

	// Spent either way. A state that has come back is finished whether the
	// sign-in worked or not, and leaving it behind would leave a replayable
	// one in the browser. This clears the browser's copy only; the server
	// keeps its own record of the nonce, written once the state has checked
	// out, because a copy kept anywhere else is refused by that record alone.
	cookie, err := r.Cookie(stateCookie)
	h.clearState(w)
	if err != nil {
		h.fail(w, r, "", "state")
		return
	}
	value, err := h.stamp.read(cookie.Value)
	if err != nil {
		h.fail(w, r, "", "state")
		return
	}
	linking := value.UserID != ""
	back := ""
	if linking {
		back = linkBack(value.Next)
	}

	query := r.URL.Query()
	// The provider refusing, or the person pressing cancel on the consent
	// screen. Its own words are not shown: they are English, sometimes
	// untranslatable, and "you did not authorise this" is the whole of it.
	if query.Get("error") != "" {
		h.fail(w, r, back, "denied")
		return
	}
	if value.Provider != provider.ID ||
		!hmac.Equal([]byte(query.Get("state")), []byte(value.Nonce)) {
		h.fail(w, r, back, "state")
		return
	}
	code := query.Get("code")
	if code == "" {
		h.fail(w, r, back, "denied")
		return
	}
	if !h.service.Enabled(provider.ID) {
		// Switched off while somebody was at the consent screen.
		h.fail(w, r, back, "unavailable")
		return
	}

	// A link belongs to the account that started it. A callback from any other
	// session is refused here, before the state is spent, so a stranger who
	// holds a copy of the cookie cannot use up the state the owner still needs.
	if linking {
		if account, signedIn := auth.UserFrom(r.Context()); !signedIn || account.ID != value.UserID {
			h.fail(w, r, back, "state")
			return
		}
	}

	// Spent before anything is asked of the provider, and not after: the
	// cookie is a ten-minute ticket, and clearing the browser's copy does not
	// reach a copy somebody else kept. Two callbacks carrying the same state
	// race on this insert, and only the one that lands goes on to the code.
	spent, err := h.service.store.SpendState(r.Context(), nil, value.Nonce, value.Expiry)
	if err != nil {
		h.fail(w, r, back, "failed")
		return
	}
	if !spent {
		h.fail(w, r, back, "state")
		return
	}

	creds, err := h.service.ResolveCredentials(r.Context(), h.Client, provider.ID)
	if err != nil {
		h.fail(w, r, back, "unavailable")
		return
	}

	identity, err := provider.Authenticate(r.Context(), h.Client,
		creds, code, h.redirectURI(r, provider.ID), value.Verifier)
	if err != nil {
		h.fail(w, r, back, providerFailure(err))
		return
	}

	if linking {
		h.finishLink(w, r, value, identity)
		return
	}

	account, err := h.service.SignIn(r.Context(), identity, h.admission(value.SignUp), h.address(r), r.UserAgent())
	if err != nil {
		// This instance wants something the provider had no way to supply.
		// Nothing has been written; the person is sent to a form and the
		// account is opened when it comes back.
		var more *MoreDetailsNeeded
		if errors.As(err, &more) {
			h.askForDetails(w, r, more.Identity, value.Next, value.SignUp)
			return
		}
		var disabledErr *auth.AccountDisabledError
		if errors.As(err, &disabledErr) && disabledErr.Reason != "" {
			h.failWithReason(w, r, "", "disabled", disabledErr.Reason)
			return
		}
		h.fail(w, r, "", signInFailure(err))
		return
	}
	next := value.Next
	if next == "" {
		next = "/"
	}
	ip, ua := h.address(r), r.UserAgent()
	token, err := h.service.auth.StartSession(r.Context(), account, ip, ua, h.service.auth.RememberedFrom(r))
	// The provider vouched for them and the account wants a code as well.
	// The pending session goes into the cookie and the sign-in page asks
	// for the rest, then carries on to wherever this was going.
	var second *auth.SecondFactorRequired
	if errors.As(err, &second) {
		h.service.auth.SetCookie(w, second.Token)
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusFound)
		return
	}
	if err != nil {
		h.fail(w, r, "", "failed")
		return
	}
	h.service.auth.SetCookie(w, token)
	// Reached only past the *SecondFactorRequired branch above, so this is
	// always a full session — a provider vouching for somebody is a first
	// factor the same way a password is (see StartSession's own comment).
	h.service.auth.AttachDevice(r.Context(), w, r, account, token, ip, ua)
	http.Redirect(w, r, next, http.StatusFound)
}

// askForDetails parks the sign-in and sends the browser to the form.
func (h *Handlers) askForDetails(
	w http.ResponseWriter, r *http.Request, identity Identity, next string, passedSignUp bool,
) {
	ticket, err := h.pending.issuePending(pending{
		Provider: identity.Provider,
		Subject:  identity.Subject,
		Login:    identity.Login,
		Name:     identity.Name,
		Email:    identity.Email,
		Next:     safeNext(next),
		SignUp:   passedSignUp,
		Expiry:   time.Now().Add(pendingTTL).UnixMilli(),
	})
	if err != nil {
		h.fail(w, r, "", "failed")
		return
	}
	h.setPending(w, ticket)
	http.Redirect(w, r, "/oauth/complete", http.StatusFound)
}

// pendingSignup is what the form reads before it draws: who the provider said
// this is, and what this instance still wants.
func (h *Handlers) pendingSignup(w http.ResponseWriter, r *http.Request) error {
	held, err := h.heldSignup(r)
	if err != nil {
		return err
	}
	missing, err := h.service.auth.MissingFor(r.Context(), nil, held.Email)
	if err != nil {
		return httpx.Internal(err)
	}

	name := ""
	if provider := ByID(held.Provider); provider != nil {
		name = provider.Name
	}
	// A subject bound to an account field has already answered the question
	// this form exists to ask. The field arrives filled with what the provider
	// proved, and the account opens with that value whatever is typed into the
	// field, so the form shows it rather than asking for it.
	suggested := h.service.SuggestedFields(held.Provider, held.Subject)
	needsPassword := h.service.settings.Bool(settings.OAuthAllowPassword)
	passwordRequired := h.service.settings.Bool(settings.OAuthRequirePassword)
	usernameRequired := h.service.settings.Bool(settings.OAuthRequireUsername)

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"provider":      held.Provider,
		"provider_name": name,
		// What the provider calls them, so the form can say whose sign-in
		// this is finishing rather than asking a stranger for details.
		"login":              held.Login,
		"suggested_username": suggestedUsername(held.Login, held.Name),
		"fields":             suggested,
		"email":              held.Email,
		"needs":              map[string]any{"fields": nonNil(missing.Fields), "email": missing.Email, "invite": missing.Invite},
		"needs_password":     needsPassword,
		"password_required":  passwordRequired,
		"username_required":  usernameRequired,
		// The same two things the sign-up form says about an address, for the
		// same reason: they are worth knowing before typing rather than after.
		"email_domains": h.service.emailDomains(),
		"verify_email":  h.service.verificationRequired(),
	})
}

func (h *Handlers) completeSignup(w http.ResponseWriter, r *http.Request) error {
	held, err := h.heldSignup(r)
	if err != nil {
		return err
	}

	var body struct {
		Username string            `json:"username"`
		Password string            `json:"password"`
		Fields   map[string]string `json:"fields"`
		Email    string            `json:"email"`
		Invite   string            `json:"invite_code"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 8*1024); err != nil {
		return err
	}

	account, err := h.service.Complete(r.Context(), Identity{
		Provider: held.Provider,
		Subject:  held.Subject,
		Login:    held.Login,
		Name:     held.Name,
		Email:    held.Email,
	}, Details{
		Username: body.Username,
		Password: body.Password,
		Fields:   body.Fields,
		Email:    body.Email,
		Invite:   body.Invite,
	}, h.admission(held.SignUp), h.address(r), r.UserAgent())
	if err != nil {
		return completionError(err)
	}

	next := held.Next
	if next == "" {
		next = "/"
	}
	// An account this form just opened has no second step yet — but the
	// completion can also land on an existing account by address, and that
	// one may.
	ip, ua := h.address(r), r.UserAgent()
	token, err := h.service.auth.StartSession(r.Context(), account, ip, ua, h.service.auth.RememberedFrom(r))
	var second *auth.SecondFactorRequired
	if errors.As(err, &second) {
		h.clearPending(w)
		h.service.auth.SetCookie(w, second.Token)
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"redirect": "/login?next=" + url.QueryEscape(next)})
	}
	if err != nil {
		return httpx.Internal(err)
	}
	h.clearPending(w)
	h.service.auth.SetCookie(w, token)
	// Reached only past the *SecondFactorRequired branch above, so this is
	// always a full session.
	h.service.auth.AttachDevice(r.Context(), w, r, account, token, ip, ua)
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"redirect": next})
}

// heldSignup reads the cookie the callback left behind. An absent or expired
// one is the ordinary case of somebody opening the page later, not an error
// worth a stack trace.
func (h *Handlers) heldSignup(r *http.Request) (pending, error) {
	stale := httpx.BadRequest("That sign-in is no longer in progress. Start again from the sign-in page.")
	cookie, err := r.Cookie(pendingCookie)
	if err != nil {
		return pending{}, stale
	}
	held, err := h.pending.readPending(cookie.Value)
	if err != nil {
		return pending{}, stale
	}
	return held, nil
}

// finishLink attaches a provider to the account that started the flow.
//
// The account is the one named in the signed state, and it has to still be
// the one holding the session: a browser that signed out and in as somebody
// else between the two halves must not hand that somebody else the
// connection.
func (h *Handlers) finishLink(w http.ResponseWriter, r *http.Request, value state, identity Identity) {
	back := linkBack(value.Next)
	account, signedIn := auth.UserFrom(r.Context())
	if !signedIn || account.ID != value.UserID {
		h.fail(w, r, back, "state")
		return
	}
	if err := h.service.Connect(r.Context(), account.ID, identity); err != nil {
		if errors.Is(err, ErrAlreadyLinked) {
			h.fail(w, r, back, "already_linked")
			return
		}
		h.fail(w, r, back, "failed")
		return
	}
	// Back where it was started, settings when it named no page: the way this
	// flow has always landed. The binding gate starts its own link with its
	// destination as next, so a connection made to satisfy that gate belongs
	// there, not at a settings screen the gate would immediately bounce it out
	// of again.
	http.Redirect(w, r, withQuery(back, "oauth", "connected"), http.StatusFound)
}

// fail sends the browser to a page that can say what happened.
//
// A code rather than a sentence, for the reason every other refusal on this
// path carries one: the server has no idea what language the reader has the
// interface in.
//
// back is the page a link returns to, and empty for a sign-in, which goes to
// its own card or to the sign-up form.
func (h *Handlers) fail(w http.ResponseWriter, r *http.Request, back, code string) {
	h.failWithReason(w, r, back, code, "")
}

func (h *Handlers) failWithReason(w http.ResponseWriter, r *http.Request, back, code, reason string) {
	page := "/login"
	if back != "" {
		page = back
	} else if r.URL.Query().Get("register") == "1" {
		page = "/register"
	}
	u := withQuery(page, "oauth_error", code)
	if reason != "" {
		u = withQuery(u, "ban_reason", reason)
	}
	http.Redirect(w, r, u, http.StatusFound)
}

// linkBack is the page a link returns to, whether it worked or not: the page it
// was started from when that is one of this site's, and the settings screen it
// has always returned to otherwise.
func linkBack(next string) string {
	if page := safeNext(next); page != "" {
		return page
	}
	return "/settings"
}

// withQuery adds one parameter to a page of this site. It goes before the
// fragment, because a browser reads everything after '#' as the fragment, and a
// page that already has a query keeps it.
func withQuery(page, key, value string) string {
	path, fragment, hasFragment := strings.Cut(page, "#")
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	out := path + sep + key + "=" + url.QueryEscape(value)
	if hasFragment {
		out += "#" + fragment
	}
	return out
}

func providerFailure(err error) string {
	switch {
	case errors.Is(err, ErrDenied):
		return "denied"
	case errors.Is(err, ErrNotConfigured):
		return "unavailable"
	default:
		return "provider"
	}
}

// completionError words the refusals the form can do something about. They
// are the sign-up form's own refusals, because this is the sign-up form with
// the parts a provider already answered taken out.
func completionError(err error) error {
	var throttled *auth.SignupThrottleError
	if errors.As(err, &throttled) {
		return httpx.TooManyRequests("signups_throttled",
			"Too many accounts have been created just now. Try again shortly.").
			WithDetails(map[string]any{
				"retry_after_seconds": int(throttled.RetryAfter.Seconds()) + 1,
			})
	}
	var domain *auth.EmailDomainError
	if errors.As(err, &domain) {
		return httpx.BadRequestCode("email_domain", "%s", domain.Error()).
			WithDetails(map[string]any{"allowed_domains": domain.Allowed})
	}
	if fieldErr := auth.FieldHTTPError(err); fieldErr != nil {
		return fieldErr
	}
	switch {
	case errors.Is(err, usercheck.ErrDisposable):
		return httpx.BadRequestCode("disposable_email", "Disposable email addresses cannot be used here.")
	case errors.Is(err, usercheck.ErrUnavailable), errors.Is(err, usercheck.ErrNotConfigured):
		return httpx.UnavailableCode("email_screening_unavailable", "Email screening is temporarily unavailable. Try again shortly.")
	}
	switch {
	case errors.Is(err, user.ErrUsernameTaken):
		return httpx.Conflict("username_taken", "That username is already taken.")
	case errors.Is(err, user.ErrInvalidUsername):
		return httpx.BadRequestCode("invalid_username", "That username is not valid.")
	case errors.Is(err, user.ErrEmailTaken):
		return httpx.Conflict("email_taken", "That email address is already registered.")
	case errors.Is(err, user.ErrInvalidEmail):
		return httpx.BadRequestCode("invalid_email", "That email address is not valid.")
	case errors.Is(err, auth.ErrEmailRequired):
		return httpx.BadRequestCode("email_required", "An email address is required on this server.")
	case errors.Is(err, auth.ErrRegistrationClosed):
		return httpx.ForbiddenCode("registration_closed", "Registration is closed on this server.")
	case errors.Is(err, auth.ErrThirdPartyOnlyRegistration):
		return httpx.ForbiddenCode("third_party_only_registration", "Registration is only permitted via third-party providers.")
	case errors.Is(err, auth.ErrInviteRequired):
		return httpx.BadRequestCode("invite_required", "An invite code is required to register here.")
	case errors.Is(err, auth.ErrInviteInvalid):
		return httpx.BadRequestCode("invite_invalid", "That invite code is not valid.")
	case errors.Is(err, ErrSignupClosed):
		return httpx.ForbiddenCode("signup_closed",
			"This server does not open accounts from a provider sign-in.")
	case errors.Is(err, ErrAddressTaken):
		return httpx.Conflict("address_taken", "An account here already uses that address.")
	case errors.Is(err, auth.ErrSignupIPBlocked):
		return httpx.ForbiddenCode("signup_ip_blocked", "You have been blocked from registering.")
	case errors.Is(err, ErrSignupChallengeRequired):
		return httpx.ForbiddenCode("signup_challenge_required",
			"This server asks for a human check before it opens an account. Start from the register page.")
	case errors.Is(err, ErrSignupRefused):
		return httpx.ForbiddenCode("signup_refused", "This sign-up was refused on this server.")
	case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong):
		return httpx.BadRequestCode("invalid_password", "%s", err.Error())
	case errors.Is(err, ErrPasswordNotAllowed):
		return httpx.ForbiddenCode("password_not_allowed", "Password setting is not enabled on this server.")
	case errors.Is(err, auth.ErrAccountDisabled):
		reason := ""
		var disabledErr *auth.AccountDisabledError
		if errors.As(err, &disabledErr) {
			reason = disabledErr.Reason
		}
		msg := "This account has been banned. Contact an administrator."
		if reason != "" {
			msg = fmt.Sprintf("This account has been banned: %s", reason)
		}
		resp := httpx.ForbiddenCode("account_banned", msg)
		if reason != "" {
			resp = resp.WithDetails(map[string]any{"ban_reason": reason})
		}
		return resp
	default:
		return httpx.Internal(err)
	}
}

func signInFailure(err error) string {
	var throttled *auth.SignupThrottleError
	if errors.As(err, &throttled) {
		return "throttled"
	}
	var domain *auth.EmailDomainError
	if errors.As(err, &domain) {
		return "domain"
	}
	if errors.Is(err, usercheck.ErrDisposable) {
		return "disposable_email"
	}
	if errors.Is(err, usercheck.ErrUnavailable) || errors.Is(err, usercheck.ErrNotConfigured) {
		return "email_screening_unavailable"
	}
	switch {
	case errors.Is(err, auth.ErrThirdPartyOnlyRegistration):
		return "third_party_only"
	case errors.Is(err, ErrAddressTaken):
		return "address_taken"
	case errors.Is(err, ErrSignupClosed):
		return "signup_closed"
	case errors.Is(err, auth.ErrRegistrationClosed):
		return "registration_closed"
	case errors.Is(err, auth.ErrAccountDisabled):
		return "disabled"
	case errors.Is(err, auth.ErrSignupIPBlocked):
		return "ip_blocked"
	case errors.Is(err, ErrSignupChallengeRequired):
		return "signup_challenge_required"
	case errors.Is(err, ErrSignupRefused):
		return "signup_refused"
	case errors.Is(err, auth.ErrEmailRequired):
		return "email_required"
	case errors.Is(err, user.ErrFieldRequired):
		var fieldErr *user.FieldError
		if errors.As(err, &fieldErr) {
			return fieldErr.Key + "_required"
		}
		return "failed"
	case errors.Is(err, user.ErrEmailTaken):
		return "address_taken"
	default:
		return "failed"
	}
}

// --- the account's own screen -------------------------------------------------

func (h *Handlers) connections(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	linked, hasPassword, err := h.service.Connections(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}

	offered := make([]map[string]any, 0, len(Providers()))
	for _, provider := range Providers() {
		offered = append(offered, map[string]any{
			"id":      provider.ID,
			"name":    h.service.DisplayName(provider.ID),
			"enabled": h.service.Enabled(provider.ID),
		})
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"connections": linked,
		"providers":   offered,
		// What decides whether a connection may be removed, and whether the
		// password box says "set" or "change".
		"has_password": hasPassword,
	})
}

func (h *Handlers) disconnect(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	provider := ByID(r.PathValue("provider"))
	if provider == nil {
		return httpx.NotFound("No such sign-in provider.")
	}

	switch err := h.service.Disconnect(r.Context(), account.ID, provider.ID); {
	case err == nil:
		return httpx.NoContent(w)
	case errors.Is(err, ErrLastWayIn):
		return httpx.Conflict("last_way_in",
			"Set a password first — this is the only way left into this account.")
	case errors.Is(err, ErrOIDCPinned):
		return httpx.Conflict("oidc_pinned",
			"An OpenID Connect connection cannot be removed; it is what proves a detail of this account.")
	case errors.Is(err, ErrNotConnected):
		return httpx.NotFound("That provider is not connected to this account.")
	default:
		return httpx.Internal(err)
	}
}

// --- where the provider sends the browser back --------------------------------

// redirectURI is the address this instance answers the provider at.
//
// Built from the request unless an operator has configured a public URL,
// which sounds like trusting a header and is not: the provider will only
// redirect to a URI registered in its own console, and the same value is sent
// again at the exchange, where it has to match the one the code was issued
// for. A caller who tampers with either only breaks their own sign-in — there
// is no host they can name here that would receive anything.
func (h *Handlers) redirectURI(r *http.Request, providerID string) string {
	if h.Origin != nil {
		return h.Origin(r) + statePath + "/callback/" + providerID
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + statePath + "/callback/" + providerID
}

func (h *Handlers) address(r *http.Request) string {
	if h.ClientIP == nil {
		return ""
	}
	return h.ClientIP(r)
}

func nonNil(keys []string) []string {
	if keys == nil {
		return []string{}
	}
	return keys
}

func suggestedUsername(login, name string) string {
	candidate := strings.TrimSpace(login)
	if user.ValidateUsername(candidate) == nil {
		return candidate
	}
	candidate = strings.TrimSpace(name)
	if user.ValidateUsername(candidate) == nil {
		return candidate
	}
	return ""
}
