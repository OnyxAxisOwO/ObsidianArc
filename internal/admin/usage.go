package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Usage and quota, for administrators: what has been spent, and the limits
// that constrain it.

// resetRequest names whose allowance to put back to full.
//
// Three scopes rather than one endpoint per scope: they differ only in which
// accounts are named, and splitting them would be three routes that have to
// agree about what a reset is.
type resetRequest struct {
	// "all" | "group" | "user"
	Scope string `json:"scope"`
	// The group or the account, when the scope names one.
	ID string `json:"id"`
}

// resetQuota puts an allowance back to its full amount.
//
// It answers with how many accounts it touched, because "reset everything" is
// the kind of thing somebody wants told back to them in numbers — and because
// resetting a group nobody is in should say so rather than look like success.
func (h *Handlers) resetQuota(w http.ResponseWriter, r *http.Request) error {
	var body resetRequest
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}

	switch body.Scope {
	case "all":
		total, err := h.users.Count(r.Context(), nil)
		if err != nil {
			return httpx.Internal(err)
		}
		if err := h.quota.ResetAll(r.Context()); err != nil {
			return httpx.Internal(err)
		}
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": total})

	case "group":
		if !isValidID(body.ID) {
			return httpx.BadRequest("A group is required.")
		}
		// Limit 0 would be the store's default page. Every member has to be
		// named, so the count comes first and asks for exactly that many.
		_, total, err := h.users.List(r.Context(), user.ListFilter{GroupID: body.ID, Limit: 1})
		if err != nil {
			return httpx.Internal(err)
		}
		members, _, err := h.users.List(r.Context(), user.ListFilter{GroupID: body.ID, Limit: total})
		if err != nil {
			return httpx.Internal(err)
		}
		ids := make([]string, 0, len(members))
		for _, member := range members {
			ids = append(ids, member.ID)
		}
		if err := h.quota.Reset(r.Context(), ids); err != nil {
			return httpx.Internal(err)
		}
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": len(ids)})

	case "user":
		if !isValidID(body.ID) {
			return httpx.BadRequest("An account is required.")
		}
		if _, err := h.users.ByID(r.Context(), nil, body.ID); err != nil {
			return translateUserError(err)
		}
		if err := h.quota.Reset(r.Context(), []string{body.ID}); err != nil {
			return httpx.Internal(err)
		}
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": 1})
	}

	return httpx.BadRequest("Reset everyone, a group, or one account.")
}

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

	// What "the most" means is the caller's to choose: the most requests, the
	// most tokens, or the most money. The ranking happens in SQL, so a top
	// fifty is the top fifty of the thing that was asked for.
	metric := r.URL.Query().Get("metric")

	byModel, err := h.usage.GroupBy(r.Context(), "model", metric, filter)
	if err != nil {
		return httpx.Internal(err)
	}
	byProvider, err := h.usage.GroupBy(r.Context(), "provider", metric, filter)
	if err != nil {
		return httpx.Internal(err)
	}
	byUser, err := h.usage.GroupBy(r.Context(), "user", metric, filter)
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
		"by_user":     byUser,
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
