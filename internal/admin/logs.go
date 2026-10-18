package admin

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/reqlog"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
)

// The request log: every call the server answered, filterable.
//
// Deliberately separate from the usage screen, which is about spend. This one
// answers "what happened" — including the requests that cost nothing because
// they were refused, which are usually the ones somebody is looking for.

func (h *Handlers) listLogs(w http.ResponseWriter, r *http.Request) error {
	filter, err := logFilterFrom(r)
	if err != nil {
		return err
	}

	// Returned as is, not wrapped as Internal: a busy aggregation slot is an
	// *httpx.Error that must reach the client as the 503 it is.
	entries, total, err := h.requests.List(r.Context(), filter)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"total":   total,
		"limit":   filter.Limit,
		"offset":  filter.Offset,
	})
}

// logFacets is what the filter controls offer: the values actually present,
// so an operator picks from what happened rather than guessing.
func (h *Handlers) logFacets(w http.ResponseWriter, r *http.Request) error {
	since, err := epochParam(r, "since")
	if err != nil {
		return err
	}

	facets, err := h.requests.Facets(r.Context(), since)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, facets)
}

// eventLogPrune is the security-log entry a prune writes. Kept beside the
// prune rather than with the account actions because only this handler uses it.
const eventLogPrune = "log_prune"

// pruneLogs deletes entries older than a number of days.
//
// Nothing calls this on a schedule unless an operator sets a retention
// period, and the endpoint requires the number to be given explicitly. The
// log is an audit trail; forgetting has to be something somebody chose, and
// said out loud.
func (h *Handlers) pruneLogs(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Days int `json:"days"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}
	if body.Days < 0 || body.Days > 3650 {
		return httpx.BadRequest("Give a number of days between 0 and 3650.")
	}
	actor := auth.MustUser(r.Context())

	// Zero means everything. That is the one prune that also takes away the
	// record of other administrators' actions, which is why it is a
	// super administrator's to do. The logs grant may still forget old
	// entries on its own schedule.
	window := time.Duration(body.Days) * 24 * time.Hour
	if body.Days == 0 {
		if !actor.IsSuperAdmin() {
			return permissionDenied()
		}
		window = time.Nanosecond
	}

	// Recorded before the delete and in the security log, which is never pruned,
	// so the record of the prune outlives the rows it removes. If the record
	// cannot be written nothing is deleted: a prune nobody can see is refused.
	// It says what was asked for, not what happened, because the delete may
	// still fail; the outcome is recorded after it succeeds.
	if h.security != nil {
		if err := h.security.Record(r.Context(), nil, securityevents.Event{
			Event: eventLogPrune, Severity: securityevents.SeverityWarning,
			ActorID: actor.ID, ActorUsername: actor.Username,
			Source: "admin", Decision: "prune",
			Reason: fmt.Sprintf("prune requested for request log entries older than %d days", body.Days),
		}); err != nil {
			return httpx.Internal(err)
		}
	}

	removed, err := h.requests.Prune(r.Context(), window)
	if err != nil {
		return httpx.Internal(err)
	}
	// The count is known only now, so it is a second entry. The rows are
	// already gone, so a write that fails here is logged rather than turned
	// into an error for the administrator. The outcome is written on a detached
	// context, so a disconnect after the rows are gone cannot lose it. The
	// request entry is not detached on purpose: a disconnect before the delete
	// aborts the prune.
	if h.security != nil {
		if err := h.recordDetached(r.Context(), securityevents.Event{
			Event: eventLogPrune, Severity: securityevents.SeverityWarning,
			ActorID: actor.ID, ActorUsername: actor.Username,
			Source: "admin", Decision: "pruned",
			Reason: fmt.Sprintf("request log entries older than %d days removed: %d", body.Days, removed),
		}); err != nil {
			slog.ErrorContext(r.Context(), "could not record a completed request-log prune", "error", err)
		}
	}
	slog.WarnContext(r.Context(), "administrator pruned the request log", "actor", actor.ID, "days", body.Days, "removed", removed)
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

func logFilterFrom(r *http.Request) (reqlog.Filter, error) {
	query := r.URL.Query()
	filter := reqlog.Filter{
		Channel:   strings.TrimSpace(query.Get("channel")),
		Method:    strings.TrimSpace(query.Get("method")),
		Outcome:   strings.ToLower(strings.TrimSpace(query.Get("outcome"))),
		Path:      strings.TrimSpace(query.Get("path")),
		ErrorCode: strings.TrimSpace(query.Get("error_code")),
	}
	// An outcome the store does not know would add no condition, and the log
	// would come back unfiltered under a filter the operator believes is in force.
	switch filter.Outcome {
	case "", reqlog.OutcomeOK, reqlog.OutcomeFailed:
	default:
		return filter, httpx.BadRequest("Outcome must be ok or failed.")
	}

	if value := strings.TrimSpace(query.Get("user_id")); value != "" {
		if !id.Valid(value) {
			return filter, httpx.BadRequest("Malformed user id.")
		}
		filter.UserID = value
	}
	if value := strings.TrimSpace(query.Get("model_id")); value != "" {
		if !id.Valid(value) {
			return filter, httpx.BadRequest("Malformed model id.")
		}
		filter.ModelID = value
	}
	if value := strings.TrimSpace(query.Get("status")); value != "" {
		status, err := strconv.Atoi(value)
		if err != nil || status < 100 || status > 599 {
			return filter, httpx.BadRequest("Status must be an HTTP status code.")
		}
		filter.Status = status
	}

	var err error
	if filter.Since, err = epochParam(r, "since"); err != nil {
		return filter, err
	}
	if filter.Until, err = epochParam(r, "until"); err != nil {
		return filter, err
	}

	filter.Limit = intParam(query.Get("limit"), 0)
	filter.Offset = intParam(query.Get("offset"), 0)
	return filter, nil
}

func epochParam(r *http.Request, name string) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, httpx.BadRequest("%s must be a timestamp in milliseconds.", name)
	}
	return value, nil
}
