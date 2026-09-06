package model

import (
	"errors"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Handlers serves the model list a signed-in user is allowed to see.
//
// Everything administrative — creating models, editing weights, granting them
// to groups — lives in internal/admin. This file is only the read the chat
// interface needs, and it deliberately returns a narrower shape than the
// admin one: no upstream model id, no provider id, no weights.
type Handlers struct {
	models *Store
}

func NewHandlers(models *Store) *Handlers { return &Handlers{models: models} }

func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.Handle("GET /api/models", auth.RequireUser(httpx.Wrap(h.list)))
}

// Public is what a regular user is told about a model. The upstream
// identifier is left out on purpose: it is an implementation detail of how
// this instance is wired, and nothing in the interface needs it — the client
// selects by our row id.
//
// So is the provider's name. It used to be here, and three screens printed
// it beside the model, which told every reader which company actually serves
// the answer — the one fact this indirection exists to keep. Removed rather
// than hidden at each call site: a field that is not in the struct cannot be
// printed by the next screen somebody adds.
type Public struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Avatar      string `json:"avatar"`
	Usable      bool   `json:"usable"`
	Capabilities
	// Absent when the model uses the built-in three, which is what lets the
	// client name those itself and keep them translated. A configured list
	// arrives named by the administrator and is shown as written.
	ReasoningTiers []PublicTier `json:"reasoning_tiers,omitempty"`
}

// PublicTier is a tier without its budget: how much thinking a name buys is
// this instance's wiring, and the client only ever sends the id back.
type PublicTier struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func toPublic(record Model) Public {
	out := Public{
		ID:           record.ID,
		DisplayName:  record.DisplayName,
		Description:  record.Description,
		Avatar:       record.Avatar,
		Usable:       record.Usable,
		Capabilities: record.Capabilities,
	}
	for _, tier := range record.ReasoningTiers {
		out.ReasoningTiers = append(out.ReasoningTiers, PublicTier{ID: tier.ID, Name: tier.Name})
	}
	return out
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	records, err := h.models.ListForUser(r.Context(), account.GroupID, account.IsAdmin())
	if err != nil {
		return httpx.Internal(err)
	}

	models := make([]Public, 0, len(records))
	for _, record := range records {
		models = append(models, toPublic(record))
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"models": models})
}

// TranslateError maps this package's sentinels onto responses. Shared with
// the chat gateway, so a model that is not permitted reads the same way
// whether the client asked for it in a listing or in a turn.
func TranslateError(err error) error {
	switch {
	case errors.Is(err, ErrNotPermitted), errors.Is(err, ErrNotFound):
		return httpx.NotFound("That model is not available.")
	case errors.Is(err, ErrDisabled):
		return httpx.Forbidden("That model is currently unavailable.")
	case errors.Is(err, ErrDuplicate):
		return httpx.Conflict("model_exists", "That model is already configured for this provider.")
	case errors.Is(err, ErrDuplicateAPIName):
		return httpx.Conflict("api_name_taken", "Another model already answers to that API name.")
	case errors.Is(err, ErrInvalidAPIName):
		return httpx.BadRequest("An API name cannot contain spaces or a slash.")
	case errors.Is(err, ErrInvalidModelID):
		return httpx.BadRequest("A model id is required.")
	case errors.Is(err, ErrInvalidName):
		return httpx.BadRequest("Display name must be 1-80 characters.")
	default:
		return httpx.Internal(err)
	}
}
