package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/reqlog"
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

	entries, total, err := h.requests.List(r.Context(), filter)
	if err != nil {
		return httpx.Internal(err)
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
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, facets)
}

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

	// Zero means everything: an operator clearing the log entirely is a
	// legitimate thing to want, and making them pass -1 for it would be a
	// puzzle rather than a safeguard.
	window := time.Duration(body.Days) * 24 * time.Hour
	if body.Days == 0 {
		window = time.Nanosecond
	}

	removed, err := h.requests.Prune(r.Context(), window)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

func logFilterFrom(r *http.Request) (reqlog.Filter, error) {
	query := r.URL.Query()
	filter := reqlog.Filter{
		Channel:   strings.TrimSpace(query.Get("channel")),
		Method:    strings.TrimSpace(query.Get("method")),
		Outcome:   strings.TrimSpace(query.Get("outcome")),
		Path:      strings.TrimSpace(query.Get("path")),
		ErrorCode: strings.TrimSpace(query.Get("error_code")),
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

	filter.Limit, _ = strconv.Atoi(query.Get("limit"))
	filter.Offset, _ = strconv.Atoi(query.Get("offset"))
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
