package card

import (
	"context"
	"errors"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Handlers is what an account does with its own cards. Creating them, and
// creating the codes that mint them, is administrative and lives in
// internal/admin.
type Handlers struct {
	store *Store
	// Performs the reset a spent card pays for. Wired in server.go, because
	// the counters belong to internal/quota and this package has no business
	// knowing they exist.
	OnSpend func(context.Context, user.User) error
}

func NewHandlers(store *Store) *Handlers { return &Handlers{store: store} }

func (h *Handlers) Routes(mux *http.ServeMux) {
	protected := func(handler httpx.Handler) http.Handler {
		return auth.RequireUser(httpx.Wrap(handler))
	}
	mux.Handle("GET /api/usage/cards", protected(h.mine))
	mux.Handle("POST /api/usage/cards/{id}/use", protected(h.use))
	mux.Handle("POST /api/usage/redeem", protected(h.redeem))
}

func (h *Handlers) mine(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	cards, err := h.store.Available(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"cards": cards})
}

// use spends a card and performs the reset it pays for.
//
// The card is marked first. If the reset then fails the card is gone and the
// allowance is not back, which is the wrong way round — but the other order
// is worse: a reset that succeeds and a card that is not marked is a card
// that resets the account again, and again, for as long as somebody keeps
// pressing. Losing one card to a database error is recoverable by an
// administrator; an unbounded reset is not.
func (h *Handlers) use(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	cardID := r.PathValue("id")
	if cardID == "" {
		return httpx.BadRequest("A card is required.")
	}

	if err := h.store.Spend(r.Context(), account.ID, cardID); err != nil {
		return translate(err)
	}
	if h.OnSpend != nil {
		if err := h.OnSpend(r.Context(), account); err != nil {
			return httpx.Internal(err)
		}
	}
	return httpx.NoContent(w)
}

func (h *Handlers) redeem(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	var body struct {
		Code string `json:"code"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}

	record, err := h.store.Redeem(r.Context(), account.ID, body.Code)
	if err != nil {
		return translate(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"card": record})
}

// TranslateError maps this package's sentinels onto responses. Exported so
// the administrative half answers the same way about the same failures.
func TranslateError(err error) error { return translate(err) }

func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound("That card does not exist.")
	case errors.Is(err, ErrUsed):
		return httpx.Conflict("card_used", "That card has already been used.")
	case errors.Is(err, ErrExpired):
		return httpx.Conflict("card_expired", "That card has expired.")
	// An unknown code and an expired one answer the same way on purpose: the
	// difference is only useful to somebody guessing at codes.
	case errors.Is(err, ErrCodeUnknown), errors.Is(err, ErrCodeExpired):
		return httpx.NotFound("That code is not valid.")
	case errors.Is(err, ErrCodeEmpty):
		return httpx.Conflict("code_empty", "That code has been fully redeemed.")
	case errors.Is(err, ErrCodeUsed):
		return httpx.Conflict("code_used", "You have already redeemed that code.")
	case errors.Is(err, ErrCodeTaken):
		return httpx.Conflict("code_exists", "That code already exists.")
	case errors.Is(err, ErrInvalidCode):
		return httpx.BadRequest("A code is required.")
	case errors.Is(err, ErrNamedBatch):
		return httpx.BadRequest("Leave the code empty when generating more than one.")
	case errors.Is(err, ErrInvalidCount):
		return httpx.BadRequest("At least one card is required.")
	}
	return httpx.Internal(err)
}
