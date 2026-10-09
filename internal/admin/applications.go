package admin

import (
	"errors"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/idp"
)

// The applications allowed to use this instance as a sign-in.
//
// Registering one is the operator's half of the arrangement: a name people
// will see on the consent screen, the callbacks the code may be sent to, and
// the claims it may ask for. The other half — whether any particular person
// lets it in — belongs to that person and is not editable here.

func (h *Handlers) listApps(w http.ResponseWriter, r *http.Request) error {
	apps, err := h.apps.ListApps(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"applications": apps,
		// What an operator has to paste into the application's own
		// configuration. Built from the request rather than stored, so it is
		// the address this instance was actually reached at.
		"issuer": h.origin(r),
		"scopes": idp.KnownScopes(),
	})
}

type appRequest struct {
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	RedirectURI *string  `json:"redirect_uris"`
	Scopes      []string `json:"scopes"`
	Trusted     *bool    `json:"trusted"`
	Disabled    *bool    `json:"disabled"`
	// Only on create. A public application holds no secret and must use PKCE.
	Public bool `json:"public"`
}

func (h *Handlers) createApp(w http.ResponseWriter, r *http.Request) error {
	var body appRequest
	if err := httpx.DecodeJSON(w, r, &body, 16*1024); err != nil {
		return err
	}
	actor := auth.MustUser(r.Context())
	trusted := body.Trusted != nil && *body.Trusted
	// Checked here rather than in the store: a new application has no stored
	// trust to race with, so only the caller's own answer matters.
	if trusted && !actor.IsSuperAdmin() {
		return trustedApplicationRefused()
	}
	record, secret, err := h.apps.CreateApp(r.Context(), idp.CreateAppInput{
		Name:         value(body.Name),
		Description:  value(body.Description),
		RedirectURIs: value(body.RedirectURI),
		Scopes:       body.Scopes,
		Trusted:      trusted,
		Public:       body.Public,
		CreatedBy:    actor.ID,
	})
	if err != nil {
		return applicationError(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"application": record,
		// Exactly once, the way an API key's token is: nothing stores it, and
		// nothing can show it again.
		"client_secret": secret,
	})
}

func (h *Handlers) updateApp(w http.ResponseWriter, r *http.Request) error {
	var body appRequest
	if err := httpx.DecodeJSON(w, r, &body, 16*1024); err != nil {
		return err
	}
	update := idp.AppUpdate{
		Name:         body.Name,
		Description:  body.Description,
		RedirectURIs: body.RedirectURI,
		Trusted:      body.Trusted,
		Disabled:     body.Disabled,
	}
	if body.Scopes != nil {
		update.Scopes = &body.Scopes
	}
	record, err := h.apps.UpdateApp(r.Context(), r.PathValue("id"), update, auth.MustUser(r.Context()).IsSuperAdmin())
	if err != nil {
		return applicationError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"application": record})
}

// rotateAppSecret issues a new secret for an application whose old one has
// leaked. Everything already issued keeps working: a token was authenticated
// when it was handed over, and taking those back is what disabling the
// application is for.
func (h *Handlers) rotateAppSecret(w http.ResponseWriter, r *http.Request) error {
	secret, err := h.apps.RotateSecret(r.Context(), r.PathValue("id"), auth.MustUser(r.Context()).IsSuperAdmin())
	if err != nil {
		return applicationError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"client_secret": secret})
}

func (h *Handlers) deleteApp(w http.ResponseWriter, r *http.Request) error {
	if err := h.apps.DeleteApp(r.Context(), r.PathValue("id"), auth.MustUser(r.Context()).IsSuperAdmin()); err != nil {
		return applicationError(err)
	}
	return httpx.NoContent(w)
}

// trustedApplicationRefused answers anybody without the super administrator's
// authority who tries to register, change or remove a trusted application. The
// message names the requirement, because a security administrator who meets it
// has no other way to tell that trust is the super administrator's decision
// rather than a grant they are missing.
func trustedApplicationRefused() error {
	return httpx.ForbiddenCode("super_admin_required",
		"Only a super administrator can register, change or remove a trusted application.")
}

func applicationError(err error) error {
	if errors.Is(err, idp.ErrNotFound) {
		return httpx.NotFound("No such application.")
	}
	if errors.Is(err, idp.ErrTrustedApplication) {
		return trustedApplicationRefused()
	}
	if message, known := idp.TranslateError(err); known {
		return httpx.BadRequest("%s", message)
	}
	if errors.Is(err, idp.ErrInvalidRequest) {
		return httpx.BadRequest("A public application has no secret to rotate.")
	}
	return httpx.Internal(err)
}

// origin is the instance's own address, or its best guess at one when the
// wiring did not supply a resolver — which is only ever a test.
func (h *Handlers) origin(r *http.Request) string {
	if h.Origin != nil {
		return h.Origin(r)
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}
