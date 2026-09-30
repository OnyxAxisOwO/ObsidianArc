package bonus

import (
	"errors"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Handlers is what an account does with its own bars. Making bars and granting
// into them is administrative and lives in internal/admin.
type Handlers struct{ store *Store }

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
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"bars": views})
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
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound("There is no such bonus.")
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
