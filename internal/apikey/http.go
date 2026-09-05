package apikey

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// Handlers is where an account manages its own keys. Every route is scoped to
// the caller: there is no "list keys for user X", not even for an
// administrator, because a key is a credential rather than a setting.
type Handlers struct {
	keys *Store
	// Reports whether this account may use the API at all, so the screen can
	// say why the list is disabled rather than offering a key that would be
	// refused on first use. Optional; nil means always allowed.
	Allowed func(*http.Request) error
}

func NewHandlers(keys *Store) *Handlers { return &Handlers{keys: keys} }

func (h *Handlers) Routes(mux *http.ServeMux) {
	protected := func(handler httpx.Handler) http.Handler {
		return auth.RequireUser(httpx.Wrap(handler))
	}

	mux.Handle("GET /api/keys", protected(h.list))
	mux.Handle("POST /api/keys", protected(h.create))
	mux.Handle("PATCH /api/keys/{id}", protected(h.update))
	mux.Handle("DELETE /api/keys/{id}", protected(h.delete))
}

type listResponse struct {
	Keys []Key `json:"keys"`
	// Whether a key created now would actually work. False when the operator
	// has the API switched off, or has not granted this account's group
	// access to it.
	Enabled bool `json:"enabled"`
	Max     int  `json:"max"`
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	keys, err := h.keys.List(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, listResponse{
		Keys:    keys,
		Enabled: h.allowed(r) == nil,
		Max:     MaxPerUser,
	})
}

type createRequest struct {
	Name    string `json:"name"`
	ModelID string `json:"model_id"`
	// Epoch millis; zero or absent means the key does not expire.
	ExpiresAt int64 `json:"expires_at"`
}

type createResponse struct {
	Key Key `json:"key"`
	// The whole point of this response. Returned here and nowhere else, ever
	// again — the server keeps only a digest of it.
	Token string `json:"token"`
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	// Checked before issuing rather than only on use: handing someone a
	// credential that cannot work is worse than refusing to.
	if err := h.allowed(r); err != nil {
		return err
	}

	var body createRequest
	if err := httpx.DecodeJSON(w, r, &body, 8*1024); err != nil {
		return err
	}

	record, token, err := h.keys.Issue(r.Context(), account.ID, body.Name, body.ModelID, body.ExpiresAt)
	if err != nil {
		return translate(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, createResponse{Key: record, Token: token})
}

type updateRequest struct {
	Name      *string `json:"name"`
	Disabled  *bool   `json:"disabled"`
	ModelID   *string `json:"model_id"`
	ExpiresAt *int64  `json:"expires_at"`
}

func (h *Handlers) update(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	keyID := r.PathValue("id")
	if !id.Valid(keyID) {
		return httpx.NotFound("No such key.")
	}

	var body updateRequest
	if err := httpx.DecodeJSON(w, r, &body, 8*1024); err != nil {
		return err
	}

	record, err := h.keys.Update(r.Context(), account.ID, keyID, Update{
		Name:      body.Name,
		Disabled:  body.Disabled,
		ModelID:   body.ModelID,
		ExpiresAt: body.ExpiresAt,
	})
	if err != nil {
		return translate(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, record)
}

func (h *Handlers) delete(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	keyID := r.PathValue("id")
	if !id.Valid(keyID) {
		return httpx.NotFound("No such key.")
	}
	if err := h.keys.Delete(r.Context(), account.ID, keyID); err != nil {
		return translate(err)
	}
	return httpx.NoContent(w)
}

func (h *Handlers) allowed(r *http.Request) error {
	if h.Allowed == nil {
		return nil
	}
	return h.Allowed(r)
}

func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound("No such key.")
	case errors.Is(err, ErrPaused):
		return httpx.Forbidden("This key is paused.")
	case errors.Is(err, ErrInvalidName):
		return httpx.BadRequest("Give the key a name of %d characters or fewer.", MaxNameChars)
	case errors.Is(err, ErrPastExpiry):
		return httpx.BadRequest("Choose an expiry in the future.")
	case errors.Is(err, ErrTooMany):
		return httpx.Conflict("too_many_keys",
			fmt.Sprintf("You already have the maximum of %d keys. Delete one first.", MaxPerUser))
	}
	return httpx.Internal(err)
}
