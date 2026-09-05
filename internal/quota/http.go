package quota

import (
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Handlers serves a user their own usage. Everything administrative — reading
// someone else's, or changing a policy — lives in internal/admin.
type Handlers struct{ service *Service }

func NewHandlers(service *Service) *Handlers { return &Handlers{service: service} }

func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.Handle("GET /api/usage/me", auth.RequireUser(httpx.Wrap(h.mine)))
}

func (h *Handlers) mine(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	summary, err := h.service.SummaryFor(r.Context(), account)
	if err != nil {
		return httpx.Internal(err)
	}
	// Read by a menu that opens often, and never by anything that must be
	// exact to the second.
	w.Header().Set("Cache-Control", "private, max-age=10")
	return httpx.WriteJSON(w, http.StatusOK, summary)
}

// TranslateError turns a quota rejection into the 429 the client expects,
// carrying which window tripped and when it frees up — the only part a user
// can act on.
func TranslateError(err error) error {
	exceeded, ok := AsExceeded(err)
	if !ok {
		return nil
	}
	return httpx.TooManyRequests("quota_exceeded", message(exceeded)).
		WithDetails(map[string]any{
			"window":    string(exceeded.Window),
			"dimension": exceeded.Dimension,
			"used":      exceeded.Used,
			"limit":     exceeded.Limit,
			"resets_at": exceeded.ResetsAt.UnixMilli(),
		}).
		WithCause(err)
}

func message(exceeded *ExceededError) string {
	switch exceeded.Window {
	case WindowRPM:
		return "You are sending requests too quickly. Try again in a moment."
	case WindowTPM:
		return "You have hit this minute's token limit. Try again in a moment."
	case Window5H:
		return "You have used your allowance for this five-hour window."
	case WindowWeek:
		return "You have used your allowance for this week."
	case WindowMonth:
		return "You have used your allowance for this month."
	default:
		return "You have reached a usage limit."
	}
}
