package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
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
		h.notifyQuotaReset(r.Context(), notify.Notification{Audience: notify.AudienceAll})
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": total})

	case "group":
		if !isValidID(body.ID) {
			return httpx.BadRequest("A group is required.")
		}
		// The count is for the answer only. Naming every member to reset them
		// meant asking the user list for as many rows as the group holds, and
		// that list clamps a page above two hundred back down to fifty — so a
		// group any larger than that was reset fifty accounts at a time while
		// reporting success. The reset selects its own rows now, and for the
		// same reason a per-member notice is not sent here: naming them all
		// would be the query this was written to avoid.
		_, total, err := h.users.List(r.Context(), user.ListFilter{GroupID: body.ID, Limit: 1})
		if err != nil {
			return httpx.Internal(err)
		}
		if err := h.quota.ResetGroup(r.Context(), body.ID); err != nil {
			return httpx.Internal(err)
		}
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": total})

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
		h.notifyQuotaReset(r.Context(), notify.Notification{Audience: notify.AudienceUser, UserID: body.ID})
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": 1})
	}

	return httpx.BadRequest("Reset everyone, a group, or one account.")
}

type cachedUsageSummary struct {
	createdAt time.Time
	data      map[string]any
}

type cachedUsageBreakdown struct {
	createdAt time.Time
	rows      []usage.Breakdown
}

func (h *Handlers) getSummaryCache(key string) map[string]any {
	h.usageCacheMu.RLock()
	defer h.usageCacheMu.RUnlock()
	if h.usageSummaryCache == nil {
		return nil
	}
	entry, ok := h.usageSummaryCache[key]
	if !ok || time.Since(entry.createdAt) >= 15*time.Second {
		return nil
	}
	return entry.data
}

func (h *Handlers) setSummaryCache(key string, data map[string]any) {
	h.usageCacheMu.Lock()
	defer h.usageCacheMu.Unlock()
	if h.usageSummaryCache == nil {
		h.usageSummaryCache = make(map[string]*cachedUsageSummary)
	}
	now := time.Now()
	if len(h.usageSummaryCache) > 50 {
		for k, v := range h.usageSummaryCache {
			if now.Sub(v.createdAt) >= 15*time.Second {
				delete(h.usageSummaryCache, k)
			}
		}
	}
	h.usageSummaryCache[key] = &cachedUsageSummary{
		createdAt: now,
		data:      data,
	}
}

func (h *Handlers) getBreakdownCache(key string) []usage.Breakdown {
	h.usageCacheMu.RLock()
	defer h.usageCacheMu.RUnlock()
	if h.usageBreakdownCache == nil {
		return nil
	}
	entry, ok := h.usageBreakdownCache[key]
	if !ok || time.Since(entry.createdAt) >= 15*time.Second {
		return nil
	}
	return entry.rows
}

func (h *Handlers) setBreakdownCache(key string, rows []usage.Breakdown) {
	h.usageCacheMu.Lock()
	defer h.usageCacheMu.Unlock()
	if h.usageBreakdownCache == nil {
		h.usageBreakdownCache = make(map[string]*cachedUsageBreakdown)
	}
	now := time.Now()
	if len(h.usageBreakdownCache) > 50 {
		for k, v := range h.usageBreakdownCache {
			if now.Sub(v.createdAt) >= 15*time.Second {
				delete(h.usageBreakdownCache, k)
			}
		}
	}
	h.usageBreakdownCache[key] = &cachedUsageBreakdown{
		createdAt: now,
		rows:      rows,
	}
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

	const hourMS = int64(time.Hour / time.Millisecond)
	const maxSpanMS = int64(365 * 24 * time.Hour / time.Millisecond)
	now := time.Now()
	nowHour := (now.UnixMilli() / hourMS) * hourMS

	// Defaults to the last thirty days: an unbounded scan of the whole ledger
	// is not what anyone opening a dashboard wants.
	defaultSince := now.AddDate(0, 0, -30).Truncate(time.Hour).UnixMilli()
	filter.Since = int64Param(query.Get("since"), defaultSince)
	filter.Until = int64Param(query.Get("until"), 0)

	// Since is rounded down to the hour.
	if filter.Since > 0 {
		filter.Since = (filter.Since / hourMS) * hourMS
	}

	// Until is rounded to the hour.
	if filter.Until > 0 {
		filter.Until = (filter.Until / hourMS) * hourMS
		if filter.Until <= filter.Since {
			filter.Until = filter.Since + hourMS
		}
	}

	// Maximum span is capped at 365 days.
	if filter.Until > 0 {
		if filter.Since <= 0 || filter.Until-filter.Since > maxSpanMS {
			filter.Since = filter.Until - maxSpanMS
		}
	} else {
		if filter.Since <= 0 || nowHour-filter.Since > maxSpanMS {
			filter.Since = nowHour - maxSpanMS
		}
	}

	filter.Limit = intParam(query.Get("limit"), 50)
	filter.Offset = intParam(query.Get("offset"), 0)
	return filter, nil
}

// zoneFrom reads the reader's distance from UTC, in minutes east — which is
// what a browser's -getTimezoneOffset() gives — so that a day on their chart
// starts at their midnight. Nowhere on Earth is more than fourteen hours out,
// and a value past that is a malformed request rather than a place.
func zoneFrom(r *http.Request) (time.Duration, error) {
	raw := r.URL.Query().Get("tz")
	if raw == "" {
		return 0, nil
	}
	minutes, err := strconv.Atoi(raw)
	if err != nil || minutes < -14*60 || minutes > 14*60 {
		return 0, httpx.BadRequest("Malformed tz.")
	}
	return time.Duration(minutes) * time.Minute, nil
}

// How much of each side the account-by-model grid shows. Enough to see who
// leans on what; past that the cells are too small to read and the answer is
// in the tables instead.
const (
	matrixUsers  = 10
	matrixModels = 8
)

// Matrix is the account-by-model grid: which of the heaviest accounts use
// which of the busiest models. The keys are sent with the cells so the
// browser draws exactly the rows the cells were counted for, rather than
// re-deriving them from a breakdown that may have been ranked differently.
type Matrix struct {
	Rows  []string     `json:"rows"`
	Cols  []string     `json:"cols"`
	Cells []usage.Cell `json:"cells"`
}

func (h *Handlers) usageSummary(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	clientIP := r.RemoteAddr
	if h.ClientIP != nil {
		clientIP = h.ClientIP(r)
	}
	rateKey := clientIP + ":" + actor.ID
	if h.UsageLimiter != nil && !h.UsageLimiter.Allow(rateKey) {
		return httpx.TooManyRequests("rate_limited", "Too many usage requests. Please try again later.")
	}

	filter, err := filterFrom(r)
	if err != nil {
		return err
	}
	zone, err := zoneFrom(r)
	if err != nil {
		return err
	}

	// What "the most" means is the caller's to choose: the most requests, the
	// most tokens, the most money, or the most people. The ranking happens in
	// SQL, so a top fifty is the top fifty of the thing that was asked for.
	metric := r.URL.Query().Get("metric")

	cacheKey := fmt.Sprintf("summary:%s:%s:%s:%s:%s:%d:%d:%d:%s",
		filter.UserID, filter.GroupID, filter.ModelID, filter.ProviderID,
		filter.Status, filter.Since, filter.Until, zone.Milliseconds(), metric)

	if cached := h.getSummaryCache(cacheKey); cached != nil {
		return httpx.WriteJSON(w, http.StatusOK, cached)
	}

	res, err, _ := h.usageSummarySF.Do(cacheKey, func() (any, error) {
		if cached := h.getSummaryCache(cacheKey); cached != nil {
			return cached, nil
		}

		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()

		release, err := httpx.AcquireAggregationSlot(dbCtx)
		if err != nil {
			return nil, err
		}
		defer release()

		// Hourly for a short range, daily for a long one: a month of hourly
		// buckets is seven hundred points nobody can read.
		bucket := time.Hour
		span := time.Duration(0)
		if filter.Since > 0 {
			if filter.Until > filter.Since {
				span = time.Duration(filter.Until-filter.Since) * time.Millisecond
			} else {
				span = time.Since(time.UnixMilli(filter.Since))
			}
		} else {
			// Since <= 0 represents all time, which is unbounded.
			// Use daily buckets to avoid hundreds of unreadable hourly buckets.
			span = 4 * 24 * time.Hour
		}
		if span > 3*24*time.Hour {
			bucket = 24 * time.Hour
		}

		var (
			totals   usage.Totals
			previous *usage.Totals
			series   []usage.Point
			heatmap  []usage.Slot
			rpm      int64
		)
		dimensions := []string{"model", "provider", "user", "group", "status"}
		ranked := make([][]usage.Breakdown, len(dimensions))
		reads := []func() error{
			func() (err error) { totals, err = h.usage.Totals(dbCtx, filter); return },
			func() (err error) { series, err = h.usage.Series(dbCtx, filter, bucket, zone); return },
			func() (err error) { heatmap, err = h.usage.Heatmap(dbCtx, filter, zone); return },
			func() (err error) { rpm, err = h.usage.CurrentRPM(dbCtx, filter); return },
		}
		for i, dimension := range dimensions {
			reads = append(reads, func() (err error) {
				ranked[i], err = h.usage.GroupBy(dbCtx, dimension, metric, filter)
				return
			})
		}
		// The same length of time immediately before, so a figure can say which
		// way it moved. Only for a bounded window: "all time" has no before.
		if filter.Since > 0 {
			before := filter
			before.Until = filter.Since
			before.Since = filter.Since - span.Milliseconds()
			reads = append(reads, func() error {
				earlier, err := h.usage.Totals(dbCtx, before)
				previous = &earlier
				return err
			})
		}
		if err := together(reads...); err != nil {
			return nil, err
		}
		breakdowns := map[string][]usage.Breakdown{}
		for i, dimension := range dimensions {
			breakdowns[dimension] = ranked[i]
		}

		// After the rest, not beside it: the grid is drawn for the top of the
		// account and model rankings, so it cannot be asked for before them.
		matrix := Matrix{Rows: keysOf(breakdowns["user"], matrixUsers), Cols: keysOf(breakdowns["model"], matrixModels)}
		if matrix.Cells, err = h.usage.Cross(dbCtx, "user", "model", matrix.Rows, matrix.Cols, filter); err != nil {
			return nil, err
		}

		payload := map[string]any{
			"totals":      totals,
			"previous":    previous,
			"by_model":    breakdowns["model"],
			"by_provider": breakdowns["provider"],
			"by_user":     breakdowns["user"],
			"by_group":    breakdowns["group"],
			"by_status":   breakdowns["status"],
			"series":      series,
			"bucket_ms":   bucket.Milliseconds(),
			"heatmap":     heatmap,
			"matrix":      matrix,
			"current_rpm": rpm,
		}
		h.setSummaryCache(cacheKey, payload)
		return payload, nil
	})
	if err != nil {
		var httpxErr *httpx.Error
		if errors.As(err, &httpxErr) {
			return httpxErr
		}
		return httpx.Internal(err)
	}

	return httpx.WriteJSON(w, http.StatusOK, res)
}

// keysOf is the first n keys of a ranked breakdown, skipping the empty one a
// turn refused before its model was known is filed under.
func keysOf(rows []usage.Breakdown, n int) []string {
	keys := make([]string, 0, n)
	for _, row := range rows {
		if len(keys) == n {
			break
		}
		if row.Key != "" {
			keys = append(keys, row.Key)
		}
	}
	return keys
}

// usageBreakdown is one dimension of the summary on its own: who used a model,
// or what an account used, without the dozen other aggregates the summary
// computes. It is what a panel about one model or one account asks, and it
// should not cost what the whole page does.
func (h *Handlers) usageBreakdown(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	clientIP := r.RemoteAddr
	if h.ClientIP != nil {
		clientIP = h.ClientIP(r)
	}
	rateKey := clientIP + ":" + actor.ID
	if h.UsageLimiter != nil && !h.UsageLimiter.Allow(rateKey) {
		return httpx.TooManyRequests("rate_limited", "Too many usage requests. Please try again later.")
	}

	filter, err := filterFrom(r)
	if err != nil {
		return err
	}
	dimension := r.URL.Query().Get("dimension")
	switch dimension {
	case "model", "provider", "user", "group", "status":
	default:
		return httpx.BadRequest("Unknown dimension.")
	}
	metric := r.URL.Query().Get("metric")

	cacheKey := fmt.Sprintf("breakdown:%s:%s:%s:%s:%s:%s:%d:%d:%s",
		dimension, filter.UserID, filter.GroupID, filter.ModelID, filter.ProviderID,
		filter.Status, filter.Since, filter.Until, metric)

	if cached := h.getBreakdownCache(cacheKey); cached != nil {
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"rows": cached})
	}

	res, err, _ := h.usageBreakdownSF.Do(cacheKey, func() (any, error) {
		if cached := h.getBreakdownCache(cacheKey); cached != nil {
			return cached, nil
		}

		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()

		release, err := httpx.AcquireAggregationSlot(dbCtx)
		if err != nil {
			return nil, err
		}
		defer release()

		rows, err := h.usage.GroupBy(dbCtx, dimension, metric, filter)
		if err != nil {
			return nil, err
		}
		h.setBreakdownCache(cacheKey, rows)
		return rows, nil
	})
	if err != nil {
		var httpxErr *httpx.Error
		if errors.As(err, &httpxErr) {
			return httpxErr
		}
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"rows": res})
}

func (h *Handlers) currentRPM(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	clientIP := r.RemoteAddr
	if h.ClientIP != nil {
		clientIP = h.ClientIP(r)
	}
	rateKey := clientIP + ":" + actor.ID
	if h.UsageLimiter != nil && !h.UsageLimiter.Allow(rateKey) {
		return httpx.TooManyRequests("rate_limited", "Too many usage requests. Please try again later.")
	}

	filter, err := filterFrom(r)
	if err != nil {
		return err
	}
	rpm, err := h.usage.CurrentRPM(r.Context(), filter)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"rpm": rpm,
	})
}

func (h *Handlers) usageRecords(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	clientIP := r.RemoteAddr
	if h.ClientIP != nil {
		clientIP = h.ClientIP(r)
	}
	rateKey := clientIP + ":" + actor.ID
	if h.UsageLimiter != nil && !h.UsageLimiter.Allow(rateKey) {
		return httpx.TooManyRequests("rate_limited", "Too many usage requests. Please try again later.")
	}

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
	visible := policies[:0]
	for _, policy := range policies {
		if canPolicy(auth.MustUser(r.Context()), policy.Scope) {
			visible = append(visible, policy)
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"policies": visible})
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
	if !canPolicy(auth.MustUser(r.Context()), body.Scope) {
		return permissionDenied()
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
	if !canPolicy(auth.MustUser(r.Context()), scope) {
		return permissionDenied()
	}
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

// notifyQuotaReset tells whoever the reset was for. n names the audience and
// the account for a "user" reset; Kind and Link are filled in here so every
// call site agrees on what this event is called.
//
// Detached with its own short timeout rather than joined to the write above:
// ResetAll and Reset already committed by the time this runs, so there is no
// transaction left to share, and a slow notify write must not turn a
// successful reset into a failed request.
func (h *Handlers) notifyQuotaReset(ctx context.Context, n notify.Notification) {
	n.Kind = "quota_reset"
	n.Link = "/usage"
	h.push(ctx, n)
}
