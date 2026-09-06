package usage

import (
	"net/http"
	"strconv"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Handlers serves a user their own consumption: what they have spent in
// total, and the turns that spent it.
//
// Everything administrative — anyone else's rows, the instance-wide
// aggregates, the breakdowns by provider — lives in internal/admin. This file
// answers one question for one account, and the account is the one holding
// the session.

type Handlers struct{ store *Store }

func NewHandlers(store *Store) *Handlers { return &Handlers{store: store} }

func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.Handle("GET /api/usage/me/history", auth.RequireUser(httpx.Wrap(h.history)))
}

// MaxHistory bounds one page of turns. A reader is looking for "what did I
// just spend", not for an export; the whole ledger is a different feature and
// would be a different shape.
const MaxHistory = 100

// Public is one turn as its own author may read it.
//
// Deliberately narrower than Record. That carries the provider's name and the
// upstream model id, and neither is anything a user is told: which company
// serves an answer is the operator's business, and the model id names the
// wiring rather than the choice. A field that is not in this struct cannot be
// leaked by a screen that forgets to strip it.
type Public struct {
	ID              string  `json:"id"`
	ModelName       string  `json:"model_name"`
	ConversationID  string  `json:"conversation_id,omitempty"`
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	ReasoningTokens int     `json:"reasoning_tokens"`
	TotalTokens     int     `json:"total_tokens"`
	Credits         float64 `json:"credits"`
	Status          Status  `json:"status"`
	ErrorCode       string  `json:"error_code,omitempty"`
	StartedAt       int64   `json:"started_at"`
	FinishedAt      int64   `json:"finished_at"`
}

func toPublic(record Record) Public {
	return Public{
		ID:              record.ID,
		ModelName:       record.ModelName,
		ConversationID:  record.ConversationID,
		InputTokens:     record.InputTokens,
		OutputTokens:    record.OutputTokens,
		ReasoningTokens: record.ReasoningTokens,
		TotalTokens:     record.TotalTokens,
		Credits:         record.Credits,
		Status:          record.Status,
		ErrorCode:       record.ErrorCode,
		StartedAt:       record.StartedAt,
		FinishedAt:      record.FinishedAt,
	}
}

func (h *Handlers) history(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	// The filter is built here rather than read from the query. The one field
	// that decides whose rows these are must not be something a caller can
	// send, so there is no code path where it could be.
	limit := MaxHistory
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed < limit {
			limit = parsed
		}
	}

	totals, err := h.store.Totals(r.Context(), Filter{UserID: account.ID})
	if err != nil {
		return httpx.Internal(err)
	}
	records, _, err := h.store.List(r.Context(), Filter{UserID: account.ID, Limit: limit})
	if err != nil {
		return httpx.Internal(err)
	}

	turns := make([]Public, 0, len(records))
	for _, record := range records {
		turns = append(turns, toPublic(record))
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"totals": totals,
		"turns":  turns,
	})
}
