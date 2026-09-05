package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
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
	trustProxy  bool
}

func NewHandlers(
	service *Service,
	users *user.Store,
	groups *group.Store,
	preferences *user.PreferenceStore,
	set *settings.Service,
	trustProxy bool,
) *Handlers {
	return &Handlers{
		service:     service,
		users:       users,
		groups:      groups,
		preferences: preferences,
		settings:    set,
		trustProxy:  trustProxy,
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

	protected := func(handler httpx.Handler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			RequireUser(httpx.Wrap(handler)).ServeHTTP(w, r)
		}
	}
	mux.HandleFunc("PATCH /api/profile", protected(h.updateProfile))
	mux.HandleFunc("POST /api/profile/password", protected(h.changePassword))
	mux.HandleFunc("GET /api/preferences", protected(h.getPreferences))
	mux.HandleFunc("PATCH /api/preferences", protected(h.patchPreferences))
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
	})
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
		IP:       httpx.ClientIP(r, h.trustProxy),
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
		IP:         httpx.ClientIP(r, h.trustProxy),
		UA:         r.UserAgent(),
	})
	if err != nil {
		var limited *RateLimitError
		if errors.As(err, &limited) {
			w.Header().Set("Retry-After", strconv.Itoa(int(limited.RetryAfter.Seconds())+1))
			return httpx.TooManyRequests("too_many_attempts", limited.Error()).
				WithDetails(map[string]any{"retry_after_seconds": int(limited.RetryAfter.Seconds()) + 1})
		}
		if errors.Is(err, ErrAccountDisabled) {
			return httpx.Forbidden("This account has been disabled.")
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

// --- error translation ------------------------------------------------------

func registrationError(err error) error {
	switch {
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
