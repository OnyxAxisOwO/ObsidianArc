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
	mux.HandleFunc("POST /api/auth/register", httpx.Wrap(h.register))
	mux.HandleFunc("POST /api/auth/login", httpx.Wrap(h.login))
	mux.HandleFunc("POST /api/auth/logout", httpx.Wrap(h.logout))
	mux.HandleFunc("GET /api/auth/me", httpx.Wrap(h.me))
	// Public: whoever opens the link out of their mail has no session
	// here, and requiring one would send them to a sign-in page that
	// then loses the token.
	mux.HandleFunc("POST /api/auth/verify", httpx.Wrap(h.verifyEmail))

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
}

// --- payloads ---------------------------------------------------------------

type accountPayload struct {
	user.User
	// Flattened onto the account so the interface can label a user's group
	// without a second request.
	GroupName string `json:"group_name"`
}

func (h *Handlers) account(r *http.Request, account user.User) accountPayload {
	payload := accountPayload{User: account}
	if account.GroupID != "" {
		if found, err := h.groups.ByID(r.Context(), nil, account.GroupID); err == nil {
			payload.GroupName = found.Name
		}
	}
	return payload
}

// --- handlers ---------------------------------------------------------------

// site is what the login page reads before anyone is signed in: the instance
// name and whether it accepts registrations. Nothing here is sensitive, and
// nothing about the accounts that exist is disclosed.
func (h *Handlers) site(w http.ResponseWriter, r *http.Request) error {
	count, err := h.users.Count(r.Context(), nil)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"name":        h.settings.Get(settings.SiteName),
		"description": h.settings.Get(settings.SiteDescription),
		// An empty instance always accepts the first account, whatever the
		// setting says; that account becomes the administrator.
		"registration_enabled": count == 0 || h.settings.Bool(settings.RegistrationEnabled),
		"setup_required":       count == 0,
		// So the sign-up form can mark the field required and say which
		// addresses will be accepted, instead of finding out on submit.
		// Neither applies to the first account.
		"require_email": count > 0 && h.settings.Bool(settings.RequireEmail),
		// So the sign-up card can say a link is coming, rather than the
		// banner being the first anyone hears of it.
		"verify_email":  count > 0 && h.service.VerificationRequired(),
		"email_domains": emailDomains(count, h.settings.Get(settings.EmailDomains)),
		// What a visitor with no account gets. Served here rather than
		// from a second endpoint because the front door has to decide what
		// to draw before it can draw anything.
		"landing": h.landing(count == 0),
	})
}

func emailDomains(accounts int, raw string) []string {
	if accounts == 0 {
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
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Nickname string `json:"nickname"`
}

func (h *Handlers) register(w http.ResponseWriter, r *http.Request) error {
	var body registerRequest
	if err := httpx.DecodeJSON(w, r, &body, 8*1024); err != nil {
		return err
	}

	account, token, err := h.service.Register(r.Context(), RegisterInput{
		Username: body.Username,
		Email:    body.Email,
		Password: body.Password,
		Nickname: body.Nickname,
		IP:       httpx.ClientIP(r, h.trust),
		UA:       r.UserAgent(),
	})
	if err != nil {
		return registrationError(err)
	}

	h.service.SetCookie(w, token)
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"user": h.account(r, account)})
}

type loginRequest struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

func (h *Handlers) login(w http.ResponseWriter, r *http.Request) error {
	var body loginRequest
	if err := httpx.DecodeJSON(w, r, &body, 8*1024); err != nil {
		return err
	}

	account, token, err := h.service.Login(r.Context(), LoginInput{
		Identifier: body.Identifier,
		Password:   body.Password,
		IP:         httpx.ClientIP(r, h.trust),
		UA:         r.UserAgent(),
	})
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
		if errors.Is(err, ErrInvalidCredentials) {
			return httpx.Unauthorized("Incorrect username or password.").
				WithDetails(map[string]any{"code_detail": "invalid_credentials"})
		}
		return httpx.Internal(err)
	}

	h.service.SetCookie(w, token)
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
}

func (h *Handlers) updateProfile(w http.ResponseWriter, r *http.Request) error {
	account := MustUser(r.Context())

	var body profileRequest
	// The cap allows an inline avatar; the field itself is bounded separately
	// by the store.
	if err := httpx.DecodeJSON(w, r, &body, user.MaxAvatarChars+16*1024); err != nil {
		return err
	}

	updated, err := h.users.UpdateProfile(r.Context(), nil, account.ID, user.ProfileUpdate{
		Nickname: body.Nickname,
		Avatar:   body.Avatar,
		Bio:      body.Bio,
		Email:    body.Email,
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

// --- error translation ------------------------------------------------------

func registrationError(err error) error {
	// Coded, so the sign-up form can word these in the reader's own
	// language and say what would be acceptable.
	var throttled *SignupThrottleError
	if errors.As(err, &throttled) {
		seconds := int(throttled.RetryAfter.Seconds()) + 1
		return httpx.TooManyRequests("signups_throttled",
			"Too many accounts have been created just now. Try again shortly.").
			WithDetails(map[string]any{"retry_after_seconds": seconds})
	}
	var domain *EmailDomainError
	if errors.As(err, &domain) {
		return httpx.BadRequest("%s", domain.Error()).
			WithDetails(map[string]any{"allowed_domains": domain.Allowed})
	}

	switch {
	case errors.Is(err, ErrEmailRequired):
		return httpx.BadRequest("An email address is required to register here.")
	case errors.Is(err, ErrRegistrationClosed):
		return httpx.Forbidden("Registration is closed on this server.")
	case errors.Is(err, user.ErrUsernameTaken):
		return httpx.Conflict("username_taken", "That username is already taken.")
	case errors.Is(err, user.ErrEmailTaken):
		return httpx.Conflict("email_taken", "That email address is already registered.")
	default:
		return profileError(err)
	}
}

func profileError(err error) error {
	switch {
	case errors.Is(err, user.ErrInvalidUsername),
		errors.Is(err, user.ErrInvalidEmail),
		errors.Is(err, user.ErrNicknameTooLong),
		errors.Is(err, user.ErrBioTooLong),
		errors.Is(err, user.ErrAvatarTooLong),
		errors.Is(err, ErrPasswordTooShort),
		errors.Is(err, ErrPasswordTooLong):
		return httpx.BadRequest("%s", trimPackagePrefix(err.Error()))
	case errors.Is(err, user.ErrEmailTaken):
		return httpx.Conflict("email_taken", "That email address is already registered.")
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
