package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Handlers is the transport layer for accounts: sign up, sign in, sign out,
// who am I, and the parts of a profile a user owns.
type Handlers struct {
	service     *Service
	users       *user.Store
	groups      *group.Store
	preferences *user.PreferenceStore
	settings    *settings.Service
	trust       httpx.ProxyTrust
	// Which third-party sign-ins the front door should offer. Set by the
	// wiring rather than read here: internal/oauth is the package that knows,
	// and it is the one that imports this one, so the arrow cannot point both
	// ways. A function because an operator switches these on and off while
	// the process runs.
	SignInProviders func() []SignInProvider
}

// SignInProvider is one button on the sign-in card. Nothing secret: the whole
// of it is already in the link the button points at.
type SignInProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func NewHandlers(
	service *Service,
	users *user.Store,
	groups *group.Store,
	preferences *user.PreferenceStore,
	set *settings.Service,
	trust httpx.ProxyTrust,
) *Handlers {
	return &Handlers{
		service:     service,
		users:       users,
		groups:      groups,
		preferences: preferences,
		settings:    set,
		trust:       trust,
	}
}

// Routes mounts everything this module serves. Public endpoints and
// authenticated ones are separated here rather than inside each handler, so
// "what needs a session" is answerable by reading this function.
func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/site", httpx.Wrap(h.site))
	mux.HandleFunc("GET /api/site/logo", httpx.Wrap(h.getSiteLogo))
	mux.HandleFunc("GET /api/site/login-background/{variant}", httpx.Wrap(h.getLoginBackground))
	mux.HandleFunc("POST /api/auth/register", httpx.Wrap(h.register))
	mux.HandleFunc("POST /api/auth/login", httpx.Wrap(h.login))
	mux.HandleFunc("POST /api/auth/logout", httpx.Wrap(h.logout))
	mux.HandleFunc("GET /api/auth/me", httpx.Wrap(h.me))
	// Public: whoever opens the link out of their mail has no session
	// here, and requiring one would send them to a sign-in page that
	// then loses the token.
	mux.HandleFunc("POST /api/auth/verify", httpx.Wrap(h.verifyEmail))
	// Public for the same reason as login: what the caller holds is the
	// pending session a password bought, not a session.
	mux.HandleFunc("POST /api/auth/two-factor", httpx.Wrap(h.completeSignIn))

	protected := func(handler httpx.Handler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			RequireUser(httpx.Wrap(handler)).ServeHTTP(w, r)
		}
	}
	mux.HandleFunc("PATCH /api/profile", protected(h.updateProfile))
	mux.HandleFunc("POST /api/profile/password", protected(h.changePassword))
	mux.HandleFunc("POST /api/profile/verify/resend", protected(h.resendVerification))
	mux.HandleFunc("GET /api/preferences", protected(h.getPreferences))
	mux.HandleFunc("PATCH /api/preferences", protected(h.patchPreferences))
	mux.HandleFunc("GET /api/preferences/wallpaper", protected(h.getWallpaper))
	mux.HandleFunc("PUT /api/preferences/wallpaper", protected(h.putWallpaper))
	mux.HandleFunc("DELETE /api/preferences/wallpaper", protected(h.deleteWallpaper))
	mux.HandleFunc("GET /api/profile/two-factor", protected(h.twoFactorStatus))
	mux.HandleFunc("POST /api/profile/two-factor/setup", protected(h.beginTwoFactor))
	mux.HandleFunc("POST /api/profile/two-factor/enable", protected(h.enableTwoFactor))
	mux.HandleFunc("POST /api/profile/two-factor/disable", protected(h.disableTwoFactor))
	mux.HandleFunc("POST /api/profile/two-factor/recovery", protected(h.regenerateRecovery))
	mux.HandleFunc("POST /api/profile/two-factor/backoffice", protected(h.enterBackoffice))
	mux.HandleFunc("POST /api/profile/two-factor/backoffice/leave", protected(h.leaveBackoffice))
	mux.HandleFunc("GET /api/profile/sessions", protected(h.listSessions))
	mux.HandleFunc("DELETE /api/profile/sessions/{id}", protected(h.revokeSession))
	mux.HandleFunc("POST /api/profile/sessions/revoke-others", protected(h.revokeOtherSessions))
}

// --- payloads ---------------------------------------------------------------

type accountPayload struct {
	user.User
	// Flattened onto the account so the interface can label a user's group
	// without a second request.
	GroupName        string `json:"group_name"`
	GroupDescription string `json:"group_description"`
	// What this account's group lets it do with the interface. The client
	// needs them to decide what to draw; the server checks them again on the
	// endpoints that act, because a hidden button is not a permission.
	//
	// True when the group cannot be read at all: losing a lookup should not
	// quietly take a capability away from someone who has it.
	AllowStats                bool `json:"allow_stats"`
	AllowDeleteConversations  bool `json:"allow_delete_conversations"`
	AllowArchiveConversations bool `json:"allow_archive_conversations"`
	// An administrator always has it: the terminal is where some of the
	// backoffice's own work is done, and a group setting should not be able
	// to lock the operator out of it. The one capability that is false when
	// the group cannot be read, because that is what the terminal's own
	// endpoints answer then; the menu should not offer a door that refuses.
	AllowTerminal   bool `json:"allow_terminal"`
	GroupShowExpiry bool `json:"group_show_expiry"`
	// What the operator's two-step policy means for this account, as three
	// answers rather than the policy itself, so the client does not have to
	// know the policy's levels to draw the right thing. The server holds the
	// same line on every endpoint regardless.
	//
	// Enrol: nothing works until the second step is on.
	TwoFactorEnrol bool `json:"two_factor_enrol"`
	// Mandatory: it may not be switched off.
	TwoFactorMandatory bool `json:"two_factor_mandatory"`
	// Backoffice: the backoffice refuses this account until it enrols.
	TwoFactorBackoffice bool `json:"two_factor_backoffice"`
	// BackofficeVerify: the backoffice asks this account for a code of its
	// own, how often ("visit", "idle", "interval"; empty when it does not),
	// how many minutes that mode counts, and whether the request this came
	// with would be refused for want of one right now.
	TwoFactorBackofficeVerify  string `json:"two_factor_backoffice_verify"`
	TwoFactorBackofficeMinutes int    `json:"two_factor_backoffice_minutes"`
	TwoFactorBackofficeLocked  bool   `json:"two_factor_backoffice_locked"`
}

func (h *Handlers) account(r *http.Request, account user.User) accountPayload {
	payload := accountPayload{
		User:                      account,
		AllowStats:                true,
		AllowDeleteConversations:  true,
		AllowArchiveConversations: account.IsAdmin() || (h.settings != nil && h.settings.Bool(settings.AllowArchive)),
		AllowTerminal:             account.IsAdmin(),
		GroupShowExpiry:           true,
	}
	if h.service != nil {
		payload.TwoFactorEnrol = h.service.MustEnrolTwoFactor(account)
		payload.TwoFactorMandatory = h.service.TwoFactorMandatory(account)
		payload.TwoFactorBackoffice = h.service.BackofficeNeedsTwoFactor(account)
		if h.service.BackofficeVerifies(account) {
			payload.TwoFactorBackofficeVerify = h.service.BackofficeVerifyMode()
			payload.TwoFactorBackofficeMinutes = h.service.BackofficeMinutes()
			payload.TwoFactorBackofficeLocked = h.service.BackofficeLocked(r.Context(), account)
		}
	}
	if account.GroupID != "" {
		if found, err := h.groups.ByID(r.Context(), nil, account.GroupID); err == nil {
			payload.GroupName = found.Name
			payload.GroupDescription = found.Description
			payload.AllowStats = found.AllowStats
			payload.AllowDeleteConversations = found.AllowDeleteConversations
			payload.AllowTerminal = account.IsAdmin() || found.AllowTerminal
			payload.GroupShowExpiry = found.ShowExpiry
		}
	}
	return payload
}

// --- handlers ---------------------------------------------------------------

// site is what the login page reads before anyone is signed in: the instance
// name and whether it accepts registrations. Nothing here is sensitive, and
// nothing about the accounts that exist is disclosed.
func (h *Handlers) site(w http.ResponseWriter, r *http.Request) error {
	populated, err := h.users.Any(r.Context(), nil)
	if err != nil {
		return httpx.Internal(err)
	}
	allowArchive := true
	if h.settings != nil {
		allowArchive = h.settings.Bool(settings.AllowArchive)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"name":        h.settings.Get(settings.SiteName),
		"description": h.settings.Get(settings.SiteDescription),
		// Already resolved against the site's own name, so the tab title
		// watcher in App.vue has no fallback of its own to keep in sync with
		// this one.
		"browser_title": h.settings.BrowserTitle(),
		// An empty instance always accepts the first account, whatever the
		// setting says; that account becomes the administrator.
		"registration_enabled":        !populated || h.settings.Bool(settings.RegistrationEnabled),
		"setup_required":              !populated,
		"health_show_users":           h.settings.Bool(settings.HealthShowUsers),
		"leaderboard_show_users":      h.settings.Bool(settings.LeaderboardShowUsers),
		"allow_archive_conversations": allowArchive,
		// So the sign-up form can mark the field required and say which
		// addresses will be accepted, instead of finding out on submit.
		// Neither applies to the first account.
		"require_email":  populated && h.settings.Bool(settings.RequireEmail),
		"require_qq":     populated && h.settings.Get(settings.QQRequirement) == settings.QQRequired,
		"qq_requirement": h.qqRequirement(!populated),
		// So the sign-up card can say a link is coming, rather than the
		// banner being the first anyone hears of it.
		"verify_email":  populated && h.service.VerificationRequired(),
		"email_domains": emailDomains(populated, h.settings.Get(settings.EmailDomains)),
		// The site key is public — it is in the page's markup wherever the
		// widget renders — and the secret it pairs with never leaves the
		// server. Served only where a challenge is actually switched on, so
		// a page that has no widget to draw is not handed a key for one.
		//
		// Never for the first account: an empty instance must not be locked
		// out of its own setup by a challenge nobody has configured yet.
		"turnstile_site_key":      h.turnstileSiteKey(!populated),
		"turnstile_on_login":      populated && h.settings.Bool(settings.TurnstileOnLogin),
		"turnstile_on_signup":     populated && h.settings.Bool(settings.TurnstileOnSignup),
		"turnstile_on_api_key":    h.settings.Bool(settings.TurnstileOnAPIKey),
		"turnstile_on_redeem":     h.settings.Bool(settings.TurnstileOnRedeem),
		"turnstile_on_feedback":   h.settings.Bool(settings.TurnstileOnFeedback),
		"turnstile_on_chat_speed": h.settings.Int(settings.ChatChallengeRequests, 0) > 0,
		// So the code step can offer "don't ask again on this browser" only
		// where the operator allows it, and say for how long.
		"two_factor_remember_days": h.service.RememberDays(),
		// The sign-ins that do not start with a password here. Empty unless
		// an operator has both configured a provider and switched it on, so
		// the card draws a divider and a row of buttons only when there is
		// something to draw.
		"oauth": h.signInProviders(),
		// So the sign-up button can say what it is waiting for. A review
		// takes seconds, and a button that only says "creating account" for
		// that long reads as a form that has hung.
		"signup_review": populated && h.settings.Bool(settings.SignupReview) &&
			h.settings.Get(settings.SignupReviewModel) != "",
		// Never invite for the first account, the same exemption every other
		// mode here carries: there is nobody yet to have issued a code, and
		// the account that opens with none is the one that turns an empty
		// instance into an administered one.
		"invite_mode": settings.InviteMode(!populated || h.settings.Bool(settings.RegistrationEnabled),
			populated && h.settings.Bool(settings.InvitesRequired)),
		// Whether the sign-up form should mention that a joined account gets
		// its own code to hand to friends — a fact about the instance, not
		// about this visitor, so it is served here rather than waiting for
		// a session to ask internal/invite's own endpoint about.
		"user_invites": h.settings.Bool(settings.InvitesUserEnabled),
		// What a visitor with no account gets. Served here rather than
		// from a second endpoint because the front door has to decide what
		// to draw before it can draw anything.
		"landing": h.landing(!populated),
		// The About panel, as the operator has written it. Both may be empty,
		// which is what the client reads as "use your own wording": the panel
		// falls back to the instance name and its built-in description rather
		// than rendering a blank card.
		"about": map[string]any{
			"title": h.settings.Get(settings.AboutTitle),
			"body":  h.settings.Get(settings.AboutBody),
		},
		// The standing notice above the chat. Served here rather than from the
		// announcements endpoint because it is not an announcement: nobody has
		// a read state for it, it is not dated, and the front door needs it
		// before anyone has signed in.
		"home_notice": map[string]any{
			"text":        h.settings.Get(settings.HomeNotice),
			"dismissible": h.settings.Bool(settings.HomeNoticeDismissible),
		},
		"login_background": h.loginBackgrounds(),
		"logo_url":         h.siteLogoURL(),
	})
}

func (h *Handlers) siteLogoURL() string {
	if h.settings == nil || h.settings.SiteLogoUpdatedAt() == 0 {
		return ""
	}
	return fmt.Sprintf("/api/site/logo?v=%d", h.settings.SiteLogoUpdatedAt())
}

func (h *Handlers) getSiteLogo(w http.ResponseWriter, r *http.Request) error {
	if h.settings == nil {
		return httpx.NotFound("Not available.")
	}

	mime, data, at, err := h.settings.GetSiteLogo(r.Context())
	if err != nil {
		if errors.Is(err, settings.ErrNoLogo) {
			return httpx.NotFound("No site logo set.")
		}
		return httpx.Internal(err)
	}

	header := w.Header()
	header.Set("Content-Type", mime)
	header.Set("Content-Length", strconv.Itoa(len(data)))
	header.Set("Cache-Control", "public, max-age=31536000, immutable")
	header.Set("Content-Disposition", "inline; filename=\"logo\"")
	header.Set("ETag", fmt.Sprintf(`"%x-%x"`, at, len(data)))
	header.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	header.Set("X-Content-Type-Options", "nosniff")

	http.ServeContent(w, r, "", time.UnixMilli(at), bytes.NewReader(data))
	return nil
}

func (h *Handlers) loginBackgrounds() map[string]string {
	if h.settings == nil {
		return map[string]string{}
	}
	raw := h.settings.LoginBackgrounds()
	out := make(map[string]string, len(raw))
	for v, at := range raw {
		out[v] = fmt.Sprintf("/api/site/login-background/%s?v=%d", v, at)
	}
	return out
}

func (h *Handlers) getLoginBackground(w http.ResponseWriter, r *http.Request) error {
	if h.settings == nil {
		return httpx.NotFound("Not available.")
	}
	variant := settings.NormalizeVariant(r.PathValue("variant"))
	if !settings.ValidLoginBackgroundVariants[variant] {
		return httpx.NotFound("No such variant.")
	}

	mime, data, at, err := h.settings.GetLoginBackground(r.Context(), variant)
	if err != nil {
		if errors.Is(err, settings.ErrNoLoginBackground) {
			return httpx.NotFound("No login background set for this variant.")
		}
		return httpx.Internal(err)
	}

	header := w.Header()
	header.Set("Content-Type", mime)
	header.Set("Content-Length", strconv.Itoa(len(data)))
	header.Set("Cache-Control", "public, max-age=31536000, immutable")
	header.Set("Content-Disposition", "inline")
	header.Set("ETag", fmt.Sprintf(`"%x-%x"`, at, len(data)))
	header.Set("X-Content-Type-Options", "nosniff")

	http.ServeContent(w, r, "", time.UnixMilli(at), bytes.NewReader(data))
	return nil
}

func (h *Handlers) signInProviders() []SignInProvider {
	if h.SignInProviders == nil {
		return []SignInProvider{}
	}
	return h.SignInProviders()
}

func (h *Handlers) qqRequirement(first bool) string {
	if first {
		return settings.QQDisabled
	}
	val := h.settings.Get(settings.QQRequirement)
	if val == settings.QQOptional || val == settings.QQRequired {
		return val
	}
	return settings.QQDisabled
}

func emailDomains(populated bool, raw string) []string {
	if !populated {
		return []string{}
	}
	return ParseDomains(raw)
}

// landing is the front door's configuration, trimmed to what a client
// needs. An instance with no accounts always shows the sign-in card,
// whatever is configured: the first thing to happen has to be someone
// becoming the administrator.
func (h *Handlers) landing(setupRequired bool) map[string]any {
	mode := h.settings.Get(settings.LandingMode)
	if setupRequired || !settings.ValidLandingMode(mode) {
		mode = settings.LandingLogin
	}

	turns := h.settings.Int(settings.TrialTurns, 3)
	if turns < 1 {
		turns = 1
	}
	if turns > settings.MaxTrialTurns {
		turns = settings.MaxTrialTurns
	}

	// The trial only exists on the chat front door, and never during
	// setup. The model id is deliberately absent: the trial endpoint
	// picks it from the same setting, so a client cannot ask for one.
	trial := mode == settings.LandingChat && h.settings.Bool(settings.TrialEnabled)

	return map[string]any{
		"mode":        mode,
		"intro":       h.settings.Get(settings.LandingIntro),
		"trial":       trial,
		"trial_turns": turns,
	}
}

type verifyRequest struct {
	Token string `json:"token"`
}

func (h *Handlers) verifyEmail(w http.ResponseWriter, r *http.Request) error {
	var body verifyRequest
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}
	if _, err := h.service.Verify(r.Context(), body.Token); err != nil {
		return verificationError(err)
	}
	return httpx.NoContent(w)
}

func (h *Handlers) resendVerification(w http.ResponseWriter, r *http.Request) error {
	account := MustUser(r.Context())
	err := h.service.Resend(r.Context(), h.settings.Get(settings.SiteName), account.ID)
	if err != nil {
		return verificationError(err)
	}
	return httpx.NoContent(w)
}

func verificationError(err error) error {
	switch {
	case errors.Is(err, ErrVerificationInvalid):
		return httpx.BadRequest("That verification link is not valid.")
	case errors.Is(err, ErrVerificationExpired):
		return httpx.BadRequest("That verification link has expired. Ask for a new one.")
	case errors.Is(err, ErrAlreadyVerified):
		return httpx.Conflict("already_verified", "That address is already verified.")
	case errors.Is(err, ErrNoAddress):
		return httpx.BadRequest("This account has no email address to verify.")
	case errors.Is(err, ErrResendTooSoon):
		return httpx.TooManyRequests("resend_too_soon",
			"A link was just sent. Check the address before asking for another.")
	case errors.Is(err, mail.ErrNotConfigured), errors.Is(err, mail.ErrTLSRequired):
		return httpx.Unavailable("This server cannot send mail.")
	default:
		return httpx.Internal(err)
	}
}

type registerRequest struct {
	// The Turnstile token, where the operator has switched the challenge on.
	Turnstile string `json:"turnstile"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	QQ        string `json:"qq"`
	Password  string `json:"password"`
	Nickname  string `json:"nickname"`
	// Empty unless this instance's registration mode asks for one, or the
	// visitor arrived through a partner link and typed or carried one along
	// anyway. See Service.Register.
	InviteCode string `json:"invite_code"`
}

func (h *Handlers) register(w http.ResponseWriter, r *http.Request) error {
	var body registerRequest
	if err := httpx.DecodeJSON(w, r, &body, 8*1024); err != nil {
		return err
	}

	ip := httpx.ClientIP(r, h.trust)
	ua := r.UserAgent()
	account, token, err := h.service.Register(r.Context(), RegisterInput{
		Turnstile:  body.Turnstile,
		Username:   body.Username,
		Email:      body.Email,
		QQ:         body.QQ,
		Password:   body.Password,
		Nickname:   body.Nickname,
		IP:         ip,
		UA:         ua,
		InviteCode: body.InviteCode,
	})
	if err != nil {
		return h.registrationError(err)
	}

	h.service.SetCookie(w, token)
	// A full session every time — Register never asks for a second step, an
	// account this fresh has never had the chance to switch one on.
	h.service.AttachDevice(r.Context(), w, r, account, token, ip, ua)
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"user": h.account(r, account)})
}

type loginRequest struct {
	Turnstile  string `json:"turnstile"`
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

func (h *Handlers) login(w http.ResponseWriter, r *http.Request) error {
	var body loginRequest
	if err := httpx.DecodeJSON(w, r, &body, 8*1024); err != nil {
		return err
	}

	ip := httpx.ClientIP(r, h.trust)
	ua := r.UserAgent()
	account, token, err := h.service.Login(r.Context(), LoginInput{
		Turnstile:  body.Turnstile,
		Identifier: body.Identifier,
		Password:   body.Password,
		IP:         ip,
		UA:         ua,
		Remembered: h.service.RememberedFrom(r),
	})
	// The password was right and the account wants a code too. The pending
	// session goes into the ordinary cookie, and the answer is a 200 with no
	// account in it: nothing failed, and nothing is signed in yet.
	var second *SecondFactorRequired
	if errors.As(err, &second) {
		h.service.SetCookie(w, second.Token)
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"two_factor": true})
	}
	if err != nil {
		var limited *RateLimitError
		if errors.As(err, &limited) {
			w.Header().Set("Retry-After", strconv.Itoa(int(limited.RetryAfter.Seconds())+1))
			return httpx.TooManyRequests("too_many_attempts", limited.Error()).
				WithDetails(map[string]any{"retry_after_seconds": int(limited.RetryAfter.Seconds()) + 1})
		}
		// Coded, not just worded: the sign-in page says this in the reader's
		// own language, and the server has no idea what that is.
		if errors.Is(err, ErrAccountDisabled) {
			return httpx.ForbiddenCode("account_banned", "This account has been banned. Contact an administrator.")
		}
		if errors.Is(err, turnstile.ErrFailed) {
			return httpx.ForbiddenCode("challenge_failed",
				"The verification could not be completed. Try again.")
		}
		if errors.Is(err, turnstile.ErrUnavailable) {
			return httpx.UnavailableCode("challenge_unavailable",
				"Verification is unavailable right now. Try again shortly.")
		}
		if errors.Is(err, ErrInvalidCredentials) {
			return httpx.Unauthorized("Incorrect username or password.").
				WithDetails(map[string]any{"code_detail": "invalid_credentials"})
		}
		return httpx.Internal(err)
	}

	h.service.SetCookie(w, token)
	// Reached only past the *SecondFactorRequired branch above, so this is
	// always a full session.
	h.service.AttachDevice(r.Context(), w, r, account, token, ip, ua)
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": h.account(r, account)})
}

func (h *Handlers) logout(w http.ResponseWriter, r *http.Request) error {
	if err := h.service.Logout(r.Context(), h.service.TokenFrom(r)); err != nil {
		return httpx.Internal(err)
	}
	h.service.ClearCookie(w)
	return httpx.NoContent(w)
}

func (h *Handlers) me(w http.ResponseWriter, r *http.Request) error {
	account, ok := UserFrom(r.Context())
	if !ok {
		if SignInPending(r.Context()) {
			return signInPending()
		}
		return httpx.Unauthorized("Not signed in.")
	}
	preferences, err := h.preferences.Get(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":        h.account(r, account),
		"preferences": preferences,
	})
}

type profileRequest struct {
	Nickname *string `json:"nickname"`
	Avatar   *string `json:"avatar"`
	Bio      *string `json:"bio"`
	Email    *string `json:"email"`
	QQ       *string `json:"qq"`
}

func (h *Handlers) updateProfile(w http.ResponseWriter, r *http.Request) error {
	account := MustUser(r.Context())

	var body profileRequest
	// The cap allows an inline avatar; the field itself is bounded separately
	// by the store.
	if err := httpx.DecodeJSON(w, r, &body, user.MaxAvatarChars+16*1024); err != nil {
		return err
	}

	// Through the service rather than straight to the store: an address
	// changed here has to clear the same registration controls as one typed
	// into the sign-up form, and withdraw the confirmation it is leaving.
	updated, err := h.service.UpdateProfile(r.Context(), account.ID, user.ProfileUpdate{
		Nickname: body.Nickname,
		Avatar:   body.Avatar,
		Bio:      body.Bio,
		Email:    body.Email,
		QQ:       body.QQ,
	})
	if err != nil {
		return profileError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": h.account(r, updated)})
}

type passwordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *Handlers) changePassword(w http.ResponseWriter, r *http.Request) error {
	account := MustUser(r.Context())
	session, _ := SessionFrom(r.Context())

	var body passwordRequest
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}

	err := h.service.ChangePassword(r.Context(), account.ID, body.CurrentPassword, body.NewPassword, session.ID)
	switch {
	case err == nil:
		return httpx.NoContent(w)
	case errors.Is(err, ErrCurrentPasswordWrong):
		return httpx.Unauthorized("Current password is incorrect.")
	case errors.Is(err, ErrPasswordUnchanged):
		return httpx.BadRequest("The new password is the same as the current one.")
	case errors.Is(err, ErrPasswordTooShort), errors.Is(err, ErrPasswordTooLong):
		return httpx.BadRequest("%s", err.Error())
	default:
		return httpx.Internal(err)
	}
}

func (h *Handlers) getPreferences(w http.ResponseWriter, r *http.Request) error {
	account := MustUser(r.Context())
	stored, err := h.preferences.Get(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"preferences": stored})
}

func (h *Handlers) patchPreferences(w http.ResponseWriter, r *http.Request) error {
	account := MustUser(r.Context())

	// Decoded as raw JSON values rather than into a struct: the server stores
	// presentation state it does not interpret, and a typed struct here would
	// mean a backend change every time the interface gains a toggle.
	var patch map[string]json.RawMessage
	if err := httpx.DecodeJSON(w, r, &patch, user.MaxPreferencesBytes); err != nil {
		return err
	}

	merged, err := h.preferences.Merge(r.Context(), account.ID, patch)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"preferences": merged})
}

// --- wallpaper ----------------------------------------------------------------

// The wallpaper is served rather than inlined into the preferences document
// because that document is read on every session check, and a megabyte of
// base64 riding along with the theme would make the cheapest request the most
// expensive one.
func (h *Handlers) getWallpaper(w http.ResponseWriter, r *http.Request) error {
	account := MustUser(r.Context())

	mime, data, at, err := h.preferences.Wallpaper(r.Context(), account.ID)
	if err != nil {
		if errors.Is(err, user.ErrNoWallpaper) {
			return httpx.NotFound("No wallpaper set.")
		}
		return httpx.Internal(err)
	}

	header := w.Header()
	header.Set("Content-Type", mime)
	header.Set("Content-Length", strconv.Itoa(len(data)))
	// Private, because the URL is the same for everyone and the image is not:
	// a shared cache must not hand one person's wallpaper to another. The URL
	// carries the version, so a long max-age is safe.
	header.Set("Cache-Control", "private, max-age=31536000, immutable")
	header.Set("X-Content-Type-Options", "nosniff")

	http.ServeContent(w, r, "", time.UnixMilli(at), bytes.NewReader(data))
	return nil
}

func (h *Handlers) putWallpaper(w http.ResponseWriter, r *http.Request) error {
	account := MustUser(r.Context())

	var body struct {
		Mime string `json:"mime"`
		Data string `json:"data"`
	}
	if err := httpx.DecodeJSON(w, r, &body, user.MaxWallpaperBytes*4/3+16*1024); err != nil {
		return err
	}

	data, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		return httpx.BadRequest("Image data is not valid base64.")
	}

	at, err := h.preferences.SetWallpaper(r.Context(), account.ID, body.Mime, data)
	if err != nil {
		switch {
		case errors.Is(err, user.ErrWallpaperUnsupported):
			return httpx.BadRequest("Wallpapers must be JPEG, PNG, WebP or AVIF.")
		case errors.Is(err, user.ErrWallpaperTooLarge):
			return httpx.BadRequest("That image is too large.")
		default:
			return httpx.Internal(err)
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"url": fmt.Sprintf("/api/preferences/wallpaper?v=%d", at),
	})
}

func (h *Handlers) deleteWallpaper(w http.ResponseWriter, r *http.Request) error {
	account := MustUser(r.Context())
	if err := h.preferences.ClearWallpaper(r.Context(), account.ID); err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}

// --- signed-in devices --------------------------------------------------------
//
// A session's own id is never shown: it is the row key a stolen cookie's
// digest would also be, and showing it would turn "compare what you see here
// with what's in your browser" into a way to hand a screen-reader the same
// secret the cookie holds. sessionRef's first sixteen hex characters are
// unique in practice for the handful of sessions one account ever holds at
// once, and worthless to anyone who does not already have the full id.

type sessionPayload struct {
	ID         string `json:"id"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
	Current    bool   `json:"current"`
}

func sessionsToPayload(sessions []Session, currentID string) []sessionPayload {
	out := make([]sessionPayload, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, sessionPayload{
			ID:         sessionRef(s.ID),
			CreatedAt:  s.CreatedAt,
			LastSeenAt: s.LastSeenAt,
			IP:         s.IP,
			UserAgent:  s.UserAgent,
			Current:    s.ID == currentID,
		})
	}
	return out
}

func (h *Handlers) listSessions(w http.ResponseWriter, r *http.Request) error {
	account := MustUser(r.Context())
	current, _ := SessionFrom(r.Context())
	sessions, err := h.service.Sessions().ListByUser(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"sessions": sessionsToPayload(sessions, current.ID),
	})
}

func (h *Handlers) revokeSession(w http.ResponseWriter, r *http.Request) error {
	account := MustUser(r.Context())
	current, hasCurrent := SessionFrom(r.Context())
	ref := r.PathValue("id")
	if !ValidSessionRef(ref) {
		return httpx.BadRequest("Malformed session id.")
	}
	if hasCurrent && sessionRef(current.ID) == ref {
		return httpx.Conflict("current_session",
			`That is this session. Use "sign out other devices" instead.`)
	}
	removed, err := h.service.Sessions().DeleteByPrefixForUser(r.Context(), account.ID, ref)
	if err != nil {
		return httpx.Internal(err)
	}
	if !removed {
		return httpx.NotFound("No such session.")
	}
	return httpx.NoContent(w)
}

func (h *Handlers) revokeOtherSessions(w http.ResponseWriter, r *http.Request) error {
	account := MustUser(r.Context())
	current, hasCurrent := SessionFrom(r.Context())
	// "Others" is measured against this session. The SSH console has none, and
	// an empty id would match every row — signing out everything, including
	// the browser the command's own help promises to keep.
	if !hasCurrent || current.ID == "" {
		return httpx.Conflict("no_current_session",
			"There is no session here to keep. Sign out a specific device instead.")
	}
	if _, err := h.service.Sessions().DeleteOthers(r.Context(), account.ID, current.ID); err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}

// --- error translation ------------------------------------------------------

func (h *Handlers) registrationError(err error) error {
	// Coded, so the sign-up form can word these in the reader's own
	// language and say what would be acceptable.
	var throttled *SignupThrottleError
	if errors.As(err, &throttled) {
		seconds := int(throttled.RetryAfter.Seconds()) + 1
		return httpx.TooManyRequests("signups_throttled",
			"Too many accounts have been created just now. Try again shortly.").
			WithDetails(map[string]any{"retry_after_seconds": seconds})
	}

	switch {
	case errors.Is(err, ErrRegistrationClosed):
		return httpx.Forbidden("Registration is closed on this server.")
	case errors.Is(err, ErrInviteRequired):
		return httpx.BadRequestCode("invite_required", "An invite code is required to register here.")
	case errors.Is(err, ErrInviteInvalid):
		return httpx.BadRequestCode("invite_invalid", "That invite code is not valid.")
	case errors.Is(err, turnstile.ErrFailed):
		return httpx.ForbiddenCode("challenge_failed",
			"The verification could not be completed. Try again.")
	case errors.Is(err, turnstile.ErrUnavailable):
		// Not the visitor's fault, and a different status so a monitor can
		// tell an outage at Cloudflare from a wave of bots.
		return httpx.UnavailableCode("challenge_unavailable",
			"Verification is unavailable right now. Try again shortly.")
	case errors.Is(err, ErrSignupRefused):
		// The operator's own words travel in the details, because the client
		// falls back to its own sentence when they have not written any and
		// a server string would be English on a Chinese screen.
		return httpx.ForbiddenCode("signup_refused", "This registration was not accepted.").
			WithDetails(map[string]any{"notice": h.settings.Get(settings.SignupReviewRefusal)})
	case errors.Is(err, ErrSignupIPBlocked):
		// A code rather than a sentence, because the client says this one in
		// the reader's own language. Deliberately says nothing about the
		// limit or the window: the number is the operator's, and telling
		// somebody exactly how long to wait is telling them exactly when to
		// come back.
		return httpx.ForbiddenCode("signup_ip_blocked",
			"You have been blocked from registering.")
	case errors.Is(err, user.ErrUsernameTaken):
		return httpx.Conflict("username_taken", "That username is already taken.")
	case errors.Is(err, user.ErrEmailTaken):
		return httpx.Conflict("email_taken", "That email address is already registered.")
	case errors.Is(err, user.ErrQQTaken):
		return httpx.Conflict("qq_taken", "That QQ number is already registered.")
	default:
		return profileError(err)
	}
}

func profileError(err error) error {
	// Coded and carrying the list, so the form can word it in the reader's
	// own language and say what would be acceptable instead.
	var domain *EmailDomainError
	if errors.As(err, &domain) {
		return httpx.BadRequest("%s", domain.Error()).
			WithDetails(map[string]any{"allowed_domains": domain.Allowed})
	}

	switch {
	case errors.Is(err, ErrEmailRequired):
		return httpx.BadRequest("An email address is required on this server.")
	case errors.Is(err, user.ErrQQRequired):
		return httpx.BadRequest("A QQ number is required on this server.")
	case errors.Is(err, user.ErrInvalidUsername),
		errors.Is(err, user.ErrInvalidEmail),
		errors.Is(err, user.ErrInvalidQQ),
		errors.Is(err, user.ErrNicknameTooLong),
		errors.Is(err, user.ErrBioTooLong),
		errors.Is(err, user.ErrAvatarTooLong),
		errors.Is(err, ErrPasswordTooShort),
		errors.Is(err, ErrPasswordTooLong):
		return httpx.BadRequest("%s", trimPackagePrefix(err.Error()))
	case errors.Is(err, user.ErrEmailTaken):
		return httpx.Conflict("email_taken", "That email address is already registered.")
	case errors.Is(err, user.ErrQQTaken):
		return httpx.Conflict("qq_taken", "That QQ number is already registered.")
	case errors.Is(err, user.ErrNotFound):
		return httpx.NotFound("No such account.")
	default:
		return httpx.Internal(err)
	}
}

// Sentinel errors are namespaced for the log ("user: ...", "auth: ..."); the
// browser should see the sentence, not the package it came from.
func trimPackagePrefix(message string) string {
	for _, prefix := range []string{"user: ", "auth: ", "group: "} {
		if len(message) > len(prefix) && message[:len(prefix)] == prefix {
			return capitalise(message[len(prefix):])
		}
	}
	return capitalise(message)
}

func capitalise(value string) string {
	if value == "" {
		return value
	}
	if value[0] >= 'a' && value[0] <= 'z' {
		return string(value[0]-32) + value[1:]
	}
	return value
}

// turnstileSiteKey is the key the widget needs, and only where one will be
// drawn: a key served to a page with no challenge on it is a key in the
// markup for nothing.
func (h *Handlers) turnstileSiteKey(firstAccount bool) string {
	if firstAccount {
		return ""
	}
	if !h.settings.Bool(settings.TurnstileOnSignup) &&
		!h.settings.Bool(settings.TurnstileOnLogin) &&
		!h.settings.Bool(settings.TurnstileOnAPIKey) &&
		!h.settings.Bool(settings.TurnstileOnRedeem) &&
		!h.settings.Bool(settings.TurnstileOnFeedback) &&
		h.settings.Int(settings.ChatChallengeRequests, 0) <= 0 {
		return ""
	}
	return h.settings.Get(settings.TurnstileSiteKey)
}
