package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
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
		h.forgetAllowanceRankings()
		h.notifyQuotaReset(r.Context(), notify.Notification{Audience: notify.AudienceAll})
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": total})

	case "group":
		if !id.Valid(body.ID) {
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
		h.forgetAllowanceRankings()
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": total})

	case "user":
		if !id.Valid(body.ID) {
			return httpx.BadRequest("An account is required.")
		}
		if err := h.authorisePolicyScope(r, quota.ScopeUser, body.ID, false); err != nil {
			return err
		}
		if err := h.quota.Reset(r.Context(), []string{body.ID}); err != nil {
			return httpx.Internal(err)
		}
		h.forgetAllowanceRankings()
		h.notifyQuotaReset(r.Context(), notify.Notification{Audience: notify.AudienceUser, UserID: body.ID})
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": 1})
	}

	return httpx.BadRequest("Reset everyone, a group, or one account.")
}

// How long a cached usage answer is served. An answer is a dozen queries over
// the ledger, and a page refreshes and every open tab asks for the same answer
// again, so a short reuse window saves that repeat work. It stays short because
// the ledger is written continuously and the figures are meant to be current.
const usageCacheTTL = 15 * time.Second

// Once a cache holds more entries than this, a write also drops the expired
// ones, so the maps do not grow with every filter an administrator has tried.
const usageCacheSweepLimit = 50

func usageCacheExpired(createdAt, now time.Time) bool {
	return now.Sub(createdAt) >= usageCacheTTL
}

// ttlCache holds answers for usageCacheTTL. It has no lock of its own: the
// summary and breakdown caches share usageCache.mu with the allowance
// generation, so a check and the insert it guards can sit under one lock.
// The zero value is ready to use.
type ttlCache[V any] struct {
	entries map[string]ttlEntry[V]
}

type ttlEntry[V any] struct {
	createdAt time.Time
	value     V
}

// get is the entry under key while it is fresh.
func (c *ttlCache[V]) get(key string, now time.Time) (V, bool) {
	entry, ok := c.entries[key]
	if !ok || usageCacheExpired(entry.createdAt, now) {
		var zero V
		return zero, false
	}
	return entry.value, true
}

// sweep drops the expired entries once the cache holds more than
// usageCacheSweepLimit, so it does not grow with every filter tried.
func (c *ttlCache[V]) sweep(now time.Time) {
	if len(c.entries) <= usageCacheSweepLimit {
		return
	}
	for k, v := range c.entries {
		if usageCacheExpired(v.createdAt, now) {
			delete(c.entries, k)
		}
	}
}

func (c *ttlCache[V]) set(key string, value V, now time.Time) {
	if c.entries == nil {
		c.entries = make(map[string]ttlEntry[V])
	}
	c.entries[key] = ttlEntry[V]{createdAt: now, value: value}
}

// deletePrefix drops every entry whose key starts with prefix, whatever its age.
func (c *ttlCache[V]) deletePrefix(prefix string) {
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			delete(c.entries, key)
		}
	}
}

// makeRoomUnder drops the oldest entry under prefix, whatever its age, when
// limit of them are cached already. except is the key about to be written: it
// replaces its own entry rather than adding one, so it does not count.
func (c *ttlCache[V]) makeRoomUnder(prefix, except string, limit int) {
	var oldestKey string
	var oldest time.Time
	cached := 0
	for k, entry := range c.entries {
		if k == except || !strings.HasPrefix(k, prefix) {
			continue
		}
		cached++
		if oldestKey == "" || entry.createdAt.Before(oldest) {
			oldestKey, oldest = k, entry.createdAt
		}
	}
	if cached >= limit {
		delete(c.entries, oldestKey)
	}
}

func (c *ttlCache[V]) len() int { return len(c.entries) }

// usageCache is the backoffice's short-lived memory of what the ledger said.
type usageCache struct {
	mu sync.RWMutex
	// The summary map also holds the allowance rankings, under
	// allowanceRankingPrefix; see allowances.go.
	summary     ttlCache[map[string]any]
	breakdown   ttlCache[[]usage.Breakdown]
	summarySF   singleflight.Group
	breakdownSF singleflight.Group
	// The allowances ranking is built once per search and group for each
	// short window, and paged from that copy; see allowances.go.
	allowanceSF singleflight.Group
	// Bumped by every write that changes what an account has left, so a ranking
	// built before the write is neither cached nor handed to a request after
	// it. Guarded by mu; see forgetAllowanceRankings.
	allowanceGeneration uint64
}

func (h *Handlers) getSummaryCache(key string) map[string]any {
	h.usageCache.mu.RLock()
	defer h.usageCache.mu.RUnlock()
	data, _ := h.usageCache.summary.get(key, time.Now())
	return data
}

func (h *Handlers) setSummaryCache(key string, data map[string]any) {
	h.usageCache.mu.Lock()
	defer h.usageCache.mu.Unlock()
	h.putSummaryLocked(key, data)
}

// putSummaryLocked is setSummaryCache for a caller that already holds
// usageCache.mu, so a check and the insert it guards can share one lock.
func (h *Handlers) putSummaryLocked(key string, data map[string]any) {
	now := time.Now()
	h.usageCache.summary.sweep(now)
	h.capAllowanceRankingsLocked(key)
	h.usageCache.summary.set(key, data, now)
}

func (h *Handlers) getBreakdownCache(key string) []usage.Breakdown {
	h.usageCache.mu.RLock()
	defer h.usageCache.mu.RUnlock()
	rows, _ := h.usageCache.breakdown.get(key, time.Now())
	return rows
}

func (h *Handlers) setBreakdownCache(key string, rows []usage.Breakdown) {
	h.usageCache.mu.Lock()
	defer h.usageCache.mu.Unlock()
	now := time.Now()
	h.usageCache.breakdown.sweep(now)
	h.usageCache.breakdown.set(key, rows, now)
}

// summaryCacheKey and breakdownCacheKey name every field an answer depends
// on, so two filters that differ anywhere never share an entry. They start
// with their own words, which no allowance ranking key does.
func summaryCacheKey(filter usage.Filter, zone time.Duration, metric string) string {
	return fmt.Sprintf("summary:%s:%s:%s:%s:%s:%d:%d:%d:%s",
		filter.UserID, filter.GroupID, filter.ModelID, filter.ProviderID,
		filter.Status, filter.Since, filter.Until, zone.Milliseconds(), metric)
}

func breakdownCacheKey(dimension string, filter usage.Filter, metric string) string {
	return fmt.Sprintf("breakdown:%s:%s:%s:%s:%s:%s:%d:%d:%s",
		dimension, filter.UserID, filter.GroupID, filter.ModelID, filter.ProviderID,
		filter.Status, filter.Since, filter.Until, metric)
}

// cachedAggregate is the shell both cached ledger reads share: the cache, then
// one build for everyone asking the same key at once, checking the cache again
// inside in case a build finished while this one queued.
//
// The build runs detached from the request with its own five seconds, because
// it is shared by everyone waiting on it and the viewer who started it leaving
// must not fail the rest. It holds an aggregation slot until its answer is
// built and stored, and an *httpx.Error from it is the answer as worded.
func cachedAggregate[V any](
	r *http.Request, flight *singleflight.Group, key string,
	lookup func() (V, bool), store func(V), build func(ctx context.Context) (V, error),
) (V, error) {
	if cached, ok := lookup(); ok {
		return cached, nil
	}
	res, err, _ := flight.Do(key, func() (any, error) {
		if cached, ok := lookup(); ok {
			return cached, nil
		}

		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()

		release, err := httpx.AcquireAggregationSlot(dbCtx)
		if err != nil {
			return nil, err
		}
		defer release()

		value, err := build(dbCtx)
		if err != nil {
			return nil, err
		}
		store(value)
		return value, nil
	})
	if err != nil {
		var zero V
		return zero, responseOr(err, internalError)
	}
	return res.(V), nil
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

	// A list, not a map: with several malformed, a map's order named a
	// different one from one request to the next.
	for _, param := range []struct{ name, value string }{
		{"user_id", filter.UserID},
		{"group_id", filter.GroupID},
		{"model_id", filter.ModelID},
		{"provider_id", filter.ProviderID},
	} {
		if param.value != "" && !id.Valid(param.value) {
			return usage.Filter{}, httpx.BadRequest("Malformed %s.", param.name)
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

// allowUsageRead spends a token from the budget every ledger read shares, keyed
// by the caller's address and account, and answers 429 when the budget is
// empty. Each read that scans the ledger takes it, so moving between endpoints
// does not get around the limit that any one of them enforces.
//
// A missing resolver falls back to the socket address, not to the empty string
// clientIP answers, because that is the key the budget has always been spent
// under when no resolver is wired; a different key would start a fresh budget.
func (h *Handlers) allowUsageRead(r *http.Request) error {
	clientIP := r.RemoteAddr
	if h.ClientIP != nil {
		clientIP = h.ClientIP(r)
	}
	if !h.UsageLimiter.Allow(clientIP + ":" + auth.MustUser(r.Context()).ID) {
		return httpx.TooManyRequests("rate_limited", "Too many usage requests. Please try again later.")
	}
	return nil
}

func (h *Handlers) usageSummary(w http.ResponseWriter, r *http.Request) error {
	if err := h.allowUsageRead(r); err != nil {
		return err
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

	cacheKey := summaryCacheKey(filter, zone, metric)
	res, err := cachedAggregate(r, &h.usageCache.summarySF, cacheKey,
		func() (map[string]any, bool) {
			cached := h.getSummaryCache(cacheKey)
			return cached, cached != nil
		},
		func(payload map[string]any) { h.setSummaryCache(cacheKey, payload) },
		func(dbCtx context.Context) (map[string]any, error) {
			return h.readUsageSummary(dbCtx, filter, zone, metric)
		})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

// readUsageSummary is the summary's reads, run inside cachedAggregate.
func (h *Handlers) readUsageSummary(dbCtx context.Context, filter usage.Filter, zone time.Duration, metric string) (map[string]any, error) {
	span, bucket := summarySpan(filter, time.Now())

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
	var err error
	if matrix.Cells, err = h.usage.Cross(dbCtx, "user", "model", matrix.Rows, matrix.Cols, filter); err != nil {
		return nil, err
	}

	return map[string]any{
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
	}, nil
}

// summarySpan is how long the window is, which also sizes the comparison
// window before it, and the bucket its series is drawn in. Hourly for a short
// range, daily for a long one: a month of hourly buckets is seven hundred
// points nobody can read.
func summarySpan(filter usage.Filter, now time.Time) (span, bucket time.Duration) {
	switch {
	case filter.Since <= 0:
		// All time is unbounded; daily buckets keep it to points a reader
		// can tell apart.
		span = 4 * 24 * time.Hour
	case filter.Until > filter.Since:
		span = time.Duration(filter.Until-filter.Since) * time.Millisecond
	default:
		span = now.Sub(time.UnixMilli(filter.Since))
	}
	bucket = time.Hour
	if span > 3*24*time.Hour {
		bucket = 24 * time.Hour
	}
	return span, bucket
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
	if err := h.allowUsageRead(r); err != nil {
		return err
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

	cacheKey := breakdownCacheKey(dimension, filter, metric)
	res, err := cachedAggregate(r, &h.usageCache.breakdownSF, cacheKey,
		func() ([]usage.Breakdown, bool) {
			cached := h.getBreakdownCache(cacheKey)
			return cached, cached != nil
		},
		func(rows []usage.Breakdown) { h.setBreakdownCache(cacheKey, rows) },
		func(dbCtx context.Context) ([]usage.Breakdown, error) {
			return h.usage.GroupBy(dbCtx, dimension, metric, filter)
		})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"rows": res})
}

func (h *Handlers) currentRPM(w http.ResponseWriter, r *http.Request) error {
	if err := h.allowUsageRead(r); err != nil {
		return err
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
	if err := h.allowUsageRead(r); err != nil {
		return err
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
	actor := auth.MustUser(r.Context())
	admins, err := h.users.AdminAccounts(r.Context(), nil)
	if err != nil {
		return httpx.Internal(err)
	}
	visible := policies[:0]
	for _, policy := range policies {
		if policyVisible(actor, policy, admins) {
			visible = append(visible, policy)
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"policies": visible})
}

// policyVisible is whether the actor may see one limit in a list. canPolicy
// only says which scopes a grant covers. A users grant does not see the limit
// on an administrator it may not manage, as the account's own panel does not
// show it that account's quota (see authorisePolicyScope for writes). admins
// is the list's administrator accounts, read once for the whole list: a limit
// that names no administrator, including one a deleted account left behind,
// is visible as it always was.
func policyVisible(actor user.User, policy quota.Policy, admins map[string]user.User) bool {
	if !canPolicy(actor, policy.Scope) {
		return false
	}
	if policy.Scope != quota.ScopeUser || actor.CanAdmin("usage") {
		return true
	}
	target, isAdmin := admins[policy.ScopeID]
	return !isAdmin || actor.CanManageAdmin(target)
}

// authorisePolicyScope applies the rule every other delegated write to an
// account answers to, to a limit written on it. canPolicy only says whether the
// actor may write user-scope limits at all, so a users grant could otherwise
// set or clear the quota of a super administrator, or of a peer it may not
// manage. Deleting the limit of an account that no longer exists is allowed,
// because that is the only way to clear the row it left behind.
func (h *Handlers) authorisePolicyScope(r *http.Request, scope quota.Scope, scopeID string, deleting bool) error {
	if scope != quota.ScopeUser {
		return nil
	}
	target, err := h.users.ByID(r.Context(), nil, scopeID)
	if err != nil {
		if deleting && errors.Is(err, user.ErrNotFound) {
			return nil
		}
		return translateUserError(err)
	}
	if target.IsAdmin() && !auth.MustUser(r.Context()).CanManageAdmin(target) {
		return permissionDenied()
	}
	return nil
}

type policyRequest struct {
	Scope   quota.Scope                   `json:"scope"`
	ScopeID string                        `json:"scope_id"`
	RPM     *int64                        `json:"rpm"`
	TPM     *int64                        `json:"tpm"`
	Windows map[quota.Window]quota.Limits `json:"windows"`
	// IfUpdatedAt, when given, is the updated_at the caller read the policy
	// at (0 for a level with no policy yet). A save replaces every window, so
	// a caller that edits one window by carrying the rest forward would
	// otherwise put back the old figures of a window somebody else changed
	// in between. Optional, so a client that sends the whole policy it means
	// keeps working unchanged.
	IfUpdatedAt *int64 `json:"if_updated_at,omitempty"`
}

// checkPolicyFigures refuses numbers that a limit cannot mean. A negative figure
// is not a small limit: enforcement reads it as no limit, and at a narrower level
// it replaces the cap an enclosing level set. Zero is the documented "no limit" for
// the per-minute override, so it stays valid there. A window at zero would show
// its figure in the badge and enforce nothing, so a window figure must be positive
// or left blank.
func checkPolicyFigures(rpm, tpm *int64, windows map[quota.Window]quota.Limits) error {
	if rpm != nil && *rpm < 0 {
		return httpx.BadRequest("The requests-per-minute limit cannot be negative.")
	}
	if tpm != nil && *tpm < 0 {
		return httpx.BadRequest("The tokens-per-minute limit cannot be negative.")
	}
	for _, window := range quota.AllowanceWindows {
		limits := windows[window]
		if limits.Requests != nil && *limits.Requests <= 0 {
			return httpx.BadRequest("The %s request limit must be greater than zero, or blank for none.", string(window))
		}
		if limits.Tokens != nil && *limits.Tokens <= 0 {
			return httpx.BadRequest("The %s token limit must be greater than zero, or blank for none.", string(window))
		}
		if limits.Credits != nil && *limits.Credits <= 0 {
			return httpx.BadRequest("The %s credit limit must be greater than zero, or blank for none.", string(window))
		}
	}
	return nil
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
	if err := checkPolicyFigures(body.RPM, body.TPM, body.Windows); err != nil {
		return err
	}

	if err := h.authorisePolicyScope(r, body.Scope, body.ScopeID, false); err != nil {
		return err
	}
	// A limit on a group that was never created, or has been deleted, is never
	// resolved for anybody and would sit on the groups page with nothing to
	// belong to. The check and the write share a transaction under the instance
	// lock that deleteGroup holds, so a delete cannot land between them and leave
	// the policy pointing at a group that is gone.
	var saved quota.Policy
	err := h.db.Tx(r.Context(), func(tx *database.Tx) error {
		// The precondition takes the same instance lock: a level with no
		// policy yet has no row to lock, and two first saves racing past an
		// unlocked check would both see it absent.
		if body.Scope == quota.ScopeGroup || body.IfUpdatedAt != nil {
			if err := settings.Lock(r.Context(), tx); err != nil {
				return err
			}
		}
		if body.Scope == quota.ScopeGroup {
			if _, err := h.groups.ByID(r.Context(), tx, body.ScopeID); err != nil {
				return translateGroupError(err)
			}
		}
		if body.IfUpdatedAt != nil {
			current, err := h.quota.Policies().GetOrEmpty(r.Context(), tx, body.Scope, body.ScopeID)
			if err != nil {
				return httpx.Internal(err)
			}
			if current.UpdatedAt != *body.IfUpdatedAt {
				return httpx.Conflict("policy_changed", "This policy was changed since it was read. Read it again and retry.")
			}
		}
		policy, err := h.quota.Policies().Save(r.Context(), tx, quota.Policy{
			Scope:   body.Scope,
			ScopeID: body.ScopeID,
			RPM:     body.RPM,
			TPM:     body.TPM,
			Windows: body.Windows,
		})
		if err != nil {
			return httpx.Internal(err)
		}
		saved = policy
		return nil
	})
	if err != nil {
		return err
	}
	h.forgetAllowanceRankings()
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

	if err := h.authorisePolicyScope(r, scope, scopeID, true); err != nil {
		return err
	}
	if err := h.quota.Policies().Delete(r.Context(), scope, scopeID); err != nil {
		return httpx.Internal(err)
	}
	h.forgetAllowanceRankings()
	return httpx.NoContent(w)
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
