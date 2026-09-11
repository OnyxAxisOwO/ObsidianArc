package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
)

// The security log records decisions rather than traffic. A refused signup,
// a temporary API restriction, and a completed challenge each answer why the
// system changed somebody's access; request_log continues to answer which
// HTTP calls happened around them.
func (h *Handlers) listSecurityEvents(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	filter := securityevents.Filter{
		Event:    strings.TrimSpace(query.Get("event")),
		Severity: securityevents.Severity(strings.TrimSpace(query.Get("severity"))),
		Decision: strings.TrimSpace(query.Get("decision")),
		Limit:    intParam(query.Get("limit"), 50),
		Offset:   intParam(query.Get("offset"), 0),
	}
	if filter.Limit <= 0 || filter.Limit > securityevents.MaxPageSize {
		filter.Limit = 50
	}
	filter.Offset = max(0, filter.Offset)
	if value := strings.TrimSpace(query.Get("user_id")); value != "" {
		if !isValidID(value) {
			return httpx.BadRequest("Malformed user id.")
		}
		filter.UserID = value
	}
	if value := strings.TrimSpace(query.Get("since")); value != "" {
		since, err := strconv.ParseInt(value, 10, 64)
		if err != nil || since < 0 {
			return httpx.BadRequest("Since must be a timestamp in milliseconds.")
		}
		filter.Since = since
	}

	events, total, err := h.security.List(r.Context(), filter)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"events": events, "total": total, "limit": filter.Limit, "offset": filter.Offset,
	})
}
