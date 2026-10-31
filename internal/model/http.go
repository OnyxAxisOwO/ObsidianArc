package model

import (
	"context"
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
// admin one: only the API-facing name, no provider id or weights.
type Handlers struct {
	models *Store
	// Set by the wiring when the operator has something to publish. Nil is
	// the ordinary case and costs nothing.
	Liveness func(ctx context.Context) map[string]Liveness
}

func NewHandlers(models *Store) *Handlers { return &Handlers{models: models} }

// Liveness is what a reader may be told about a model's health.
//
// Both fields have already been through the operator's settings by the time
// they arrive: an empty value means there is nothing to say. This package
// does not decide what may be said, and does not import the one that measures
// it — internal/health already knows about models, and a module knowing about
// both would close the loop.
type Liveness struct {
	// Present only where the operator has chosen to publish the figure.
	Uptime *float64
	// Set where the operator has a warning threshold and this model is under
	// it. Independent of Uptime: an instance can warn without publishing a
	// number, which is the more common thing to want.
	Unstable bool
}

func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.Handle("GET /api/models", auth.RequireUser(httpx.Wrap(h.list)))
}

// Public carries the same API-facing identifier as /v1/models, so a reader
// can configure another client. A configured API name keeps the upstream
// identifier private; the chat itself still selects by our row id.
//
// The provider's name is not carried here: three screens printed it
// beside the model, which told every reader which company actually serves the
// answer, the one fact this indirection exists to keep. Keeping it out of the
// struct, rather than hiding it at each call site, means a screen added later
// cannot print what it never receives.
type Public struct {
	ID          string `json:"id"`
	APIName     string `json:"api_name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Avatar      string `json:"avatar"`
	Usable      bool   `json:"usable"`
	Capabilities
	// Absent when the model uses the built-in three, which is what lets the
	// client name those itself and keep them translated. A configured list
	// arrives named by the administrator and is shown as written.
	ReasoningTiers []PublicTier `json:"reasoning_tiers,omitempty"`

	// Absent unless the operator publishes it. A share of 1, not a
	// percentage: the client decides how many digits are worth showing.
	Uptime *float64 `json:"uptime,omitempty"`
	// The model has been failing often enough that somebody deserves to know
	// before they type a long question into it.
	Unstable bool `json:"unstable,omitempty"`
}

// PublicTier is a tier without its budget: how much thinking a name buys is
// this instance's wiring, and the client only ever sends the id back.
type PublicTier struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func toPublic(record Model, live Liveness, claims int) Public {
	out := Public{
		ID:           record.ID,
		APIName:      APIRef(record.ID, record.ModelID, record.APIName, claims),
		DisplayName:  record.DisplayName,
		Description:  record.Description,
		Avatar:       record.Avatar,
		Usable:       record.Usable,
		Capabilities: record.Capabilities,
		Uptime:       live.Uptime,
		Unstable:     live.Unstable,
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

	// One lookup for the whole listing, and nothing at all when the operator
	// has neither published a figure nor asked for a warning.
	var live map[string]Liveness
	if h.Liveness != nil {
		live = h.Liveness(r.Context())
	}

	// View-only rows do not claim a name in /v1/models, so they must not
	// qualify a usable model's name in the picker either.
	claims := make(map[string]int, len(records))
	for _, record := range records {
		if record.Usable {
			claims[PublicName(record.APIName, record.ModelID)]++
		}
	}
	models := make([]Public, 0, len(records))
	for _, record := range records {
		models = append(models, toPublic(record, live[record.ID], claims[PublicName(record.APIName, record.ModelID)]))
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
	case errors.Is(err, ErrInvalidRequestOverride):
		return httpx.BadRequest("Request override must be a valid JSON object.")
	case errors.Is(err, ErrRouted):
		return httpx.Conflict("model_routed", "Another model routes to this one. Clear that route first.")
	case errors.Is(err, ErrInvalidAccess):
		return httpx.BadRequest("Access must be use, view or none.")
	case errors.Is(err, ErrInvalidReasoningStyle):
		return httpx.BadRequest("Unknown reasoning style.")
	case errors.Is(err, ErrRouteChain):
		return httpx.BadRequest("A route is one hop: that model is already routed, or another model routes to it.")
	case errors.Is(err, ErrRouteTargetMissing):
		return httpx.BadRequest("Route target does not exist.")
	default:
		return httpx.Internal(err)
	}
}
