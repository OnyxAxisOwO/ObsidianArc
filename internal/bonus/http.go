package bonus

import (
	"errors"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Handlers is what an account does with its own bars. Making bars and granting
// into them is administrative and lives in internal/admin.
type Handlers struct {
	store *Store
	// How the instance words an allowance. It is sent with the bars, like the
	// allowance's own summary sends it with the windows, because it is only ever
	// read alongside the figures it words; left nil it is the figures.
	Display func() string
}

func NewHandlers(store *Store) *Handlers { return &Handlers{store: store} }

func (h *Handlers) Routes(mux *http.ServeMux) {
	protected := func(handler httpx.Handler) http.Handler {
		return auth.RequireUser(httpx.Wrap(handler))
	}
	mux.Handle("GET /api/bonus", protected(h.mine))
	mux.Handle("PUT /api/bonus/{id}/choice", protected(h.choose))
}

func (h *Handlers) mine(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	views, err := h.store.ViewFor(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	display := "absolute"
	if h.Display != nil {
		display = h.Display()
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"bars": views, "display": display})
}

func (h *Handlers) choose(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 1024); err != nil {
		return err
	}
	if body.Enabled == nil {
		return httpx.BadRequest("Say whether to switch it on or off.")
	}
	if err := h.store.SetChoice(r.Context(), account.ID, r.PathValue("id"), *body.Enabled); err != nil {
		return TranslateError(err)
	}
	return httpx.NoContent(w)
}

// TranslateError maps this package's sentinels onto responses; exported so the
// administrative half answers the same way about the same failures.
func TranslateError(err error) error {
	switch {
	// These two wrap ErrNotFound, so they come before it: a bar that exists and is
	// off is not a bar that is missing, and a name nobody holds is not either.
	case errors.Is(err, ErrBarOff):
		return httpx.Conflict("bonus_bar_off", "That bonus is switched off. Switch it on before granting into it.")
	// A code, not the bare bad_request: the interface words this refusal in the
	// reader's language, and bad_request alone does not say which refusal it was.
	case errors.Is(err, ErrNoAccount):
		return httpx.BadRequestCode("bonus_no_account", "No account has that name or ID.")
	// Not a bad request: the request is well formed, and the damage is in the
	// stored bar. The code is what the interface words; the sentence is the
	// fallback for a reader without it.
	case errors.Is(err, ErrModelsUnreadable):
		return httpx.Conflict("bonus_models_unreadable", "This bonus's stored model list cannot be read, so the change was not saved. Delete the bonus and create it again.")
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound("There is no such bonus.")
	case errors.Is(err, ErrSharedNoGrants):
		return httpx.Conflict("bonus_shared_no_grants", "A shared pool is topped up for everyone; it is not granted to accounts.")
	case errors.Is(err, ErrNotShared):
		return httpx.Conflict("bonus_not_shared", "That bonus is not a shared pool.")
	case errors.Is(err, ErrKindChange):
		return httpx.Conflict("bonus_kind_change", "A shared pool cannot be turned into another kind of bonus, nor another kind into a pool.")
	case errors.Is(err, ErrInvalidPool):
		return httpx.BadRequest("A pool resets never, daily, weekly or monthly, with an amount above zero, and its amounts are at most a million.")
	case errors.Is(err, ErrNotChoosable):
		return httpx.Conflict("bonus_not_choosable", "This bonus is not yours to switch on or off.")
	case errors.Is(err, ErrInvalidBar):
		return httpx.BadRequest("A bonus needs a name, and a type and switch it knows.")
	case errors.Is(err, ErrInvalidAmount):
		return httpx.BadRequest("An amount is more than zero and at most a million.")
	case errors.Is(err, ErrInvalidExpiry):
		return httpx.BadRequest("An expiry has to be in the future.")
	}
	return httpx.Internal(err)
}
