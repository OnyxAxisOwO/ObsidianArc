package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
)

// Usage and quota, for administrators: what has been spent, and the limits
// that constrain it.

// filterFrom builds a ledger filter from the query string. Every value is
// validated here rather than in the store, so a malformed parameter is a 400
// and never reaches a query.
func filterFrom(r *http.Request) (usage.Filter, error) {
	query := r.URL.Query()
	filter := usage.Filter{
		UserID:     query.Get("user_id"),
		GroupID:    query.Get("group_id"),
		ModelID:    query.Get("model_id"),
		ProviderID: query.Get("provider_id"),
		Status:     usage.Status(query.Get("status")),
	}

	for name, value := range map[string]string{
		"user_id":     filter.UserID,
		"group_id":    filter.GroupID,
		"model_id":    filter.ModelID,
		"provider_id": filter.ProviderID,
	} {
		if value != "" && !id.Valid(value) {
			return usage.Filter{}, httpx.BadRequest("Malformed %s.", name)
		}
	}
	switch filter.Status {
	case "", usage.StatusOK, usage.StatusError, usage.StatusAborted, usage.StatusRejected:
	default:
		return usage.Filter{}, httpx.BadRequest("Unknown status filter.")
	}

	// Defaults to the last thirty days: an unbounded scan of the whole ledger
	// is not what anyone opening a dashboard wants.
	filter.Since = int64Param(query.Get("since"), time.Now().AddDate(0, 0, -30).UnixMilli())
	filter.Until = int64Param(query.Get("until"), 0)
	filter.Limit = intParam(query.Get("limit"), 50)
	filter.Offset = intParam(query.Get("offset"), 0)
	return filter, nil
}

func (h *Handlers) usageSummary(w http.ResponseWriter, r *http.Request) error {
	filter, err := filterFrom(r)
	if err != nil {
		return err
	}

	totals, err := h.usage.Totals(r.Context(), filter)
	if err != nil {
		return httpx.Internal(err)
	}

	byModel, err := h.usage.GroupBy(r.Context(), "model", filter)
	if err != nil {
		return httpx.Internal(err)
	}
	byProvider, err := h.usage.GroupBy(r.Context(), "provider", filter)
	if err != nil {
		return httpx.Internal(err)
	}

	// Hourly for a short range, daily for a long one: a month of hourly
	// buckets is seven hundred points nobody can read.
	bucket := time.Hour
	if filter.Since > 0 && time.Since(time.UnixMilli(filter.Since)) > 3*24*time.Hour {
		bucket = 24 * time.Hour
	}
	series, err := h.usage.Series(r.Context(), filter, bucket)
	if err != nil {
		return httpx.Internal(err)
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"totals":      totals,
		"by_model":    byModel,
		"by_provider": byProvider,
		"series":      series,
		"bucket_ms":   bucket.Milliseconds(),
	})
}

func (h *Handlers) usageRecords(w http.ResponseWriter, r *http.Request) error {
	filter, err := filterFrom(r)
	if err != nil {
		return err
	}

	records, total, err := h.usage.List(r.Context(), filter)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"records": records,
		"total":   total,
	})
}

// --- policies -------------------------------------------------------------------

func (h *Handlers) listPolicies(w http.ResponseWriter, r *http.Request) error {
	policies, err := h.quota.Policies().List(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"policies": policies})
}

type policyRequest struct {
	Scope   quota.Scope                   `json:"scope"`
	ScopeID string                        `json:"scope_id"`
	RPM     *int64                        `json:"rpm"`
	TPM     *int64                        `json:"tpm"`
	Windows map[quota.Window]quota.Limits `json:"windows"`
}

// savePolicy writes the limits at one scope. A policy is replaced wholesale
// rather than patched: the form always submits every field, and a partial
// update of a structure whose whole meaning is "which fields are set" would
// be ambiguous.
func (h *Handlers) savePolicy(w http.ResponseWriter, r *http.Request) error {
	var body policyRequest
	if err := httpx.DecodeJSON(w, r, &body, 16*1024); err != nil {
		return err
	}

	switch body.Scope {
	case quota.ScopeGlobal:
		body.ScopeID = ""
	case quota.ScopeGroup, quota.ScopeUser:
		if !id.Valid(body.ScopeID) {
			return httpx.BadRequest("A group or user must be identified.")
		}
	default:
		return httpx.BadRequest("Scope must be global, group or user.")
	}

	for window := range body.Windows {
		switch window {
		case quota.Window5H, quota.WindowWeek, quota.WindowMonth:
		default:
			return httpx.BadRequest("Unknown window %q.", string(window))
		}
	}

	saved, err := h.quota.Policies().Save(r.Context(), quota.Policy{
		Scope:   body.Scope,
		ScopeID: body.ScopeID,
		RPM:     body.RPM,
		TPM:     body.TPM,
		Windows: body.Windows,
	})
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"policy": saved})
}

func (h *Handlers) deletePolicy(w http.ResponseWriter, r *http.Request) error {
	scope := quota.Scope(r.PathValue("scope"))
	scopeID := r.URL.Query().Get("scope_id")

	switch scope {
	case quota.ScopeGlobal:
		scopeID = ""
	case quota.ScopeGroup, quota.ScopeUser:
		if !id.Valid(scopeID) {
			return httpx.BadRequest("A group or user must be identified.")
		}
	default:
		return httpx.BadRequest("Scope must be global, group or user.")
	}

	if err := h.quota.Policies().Delete(r.Context(), scope, scopeID); err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}

func intParam(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func int64Param(raw string, fallback int64) int64 {
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
