// Package leaderboard ranks the instance's accounts and models for the people
// using it, rather than for the people running it.
//
// It reads the same ledger the backoffice dashboard does — usage.Store's
// grouped aggregates — and deliberately answers a narrower question with it.
// An operator's ranking is for knowing where the money goes; a reader's is a
// bit of fun and a sense of how their own use compares, so what reaches the
// browser here is cut down to that:
//
//   - No credits. Credits are tokens times a price the operator set per
//     model, so publishing them publishes the price list.
//   - No account identifiers. The one thing a stranger could do with a ULID
//     is correlate it with something else; a place number and a name are all
//     a leaderboard needs.
//   - No providers. Which company serves an answer is the operator's
//     business, and /api/uptime keeps it back from readers for the same
//     reason.
//
// Only answered turns count. A turn that failed or was refused spent nothing
// the reader got, and a leaderboard of retries rewards the wrong thing.
package leaderboard

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The grant that lets an administrator see the board before it is published,
// and edit how it looks. Named once here so the page, the settings and this
// check cannot drift apart on spelling.
const Permission = "leaderboard"

// Periods a reader may choose, as how far back from now they reach. A fixed
// set rather than any duration a caller sends, so a request cannot ask for an
// aggregate over the whole ledger on every page load.
var periods = map[string]time.Duration{
	"day":   24 * time.Hour,
	"week":  7 * 24 * time.Hour,
	"month": 30 * 24 * time.Hour,
}

// Metrics a reader may rank by. Credits is left out on purpose — see the
// package comment.
var metrics = map[string]string{
	"tokens":   usage.MetricTokens,
	"requests": usage.MetricRequests,
}

type cachedBoard struct {
	createdAt time.Time
	userRows  []usage.Breakdown
	userInfos map[string]user.User
	modelRows []usage.Breakdown
}

type Handlers struct {
	settings *settings.Service
	usage    *usage.Store
	users    *user.Store
	models   *model.Store

	ClientIP func(*http.Request) string
	Limiter  *httpx.TokenBucketLimiter

	mu    sync.RWMutex
	cache map[string]*cachedBoard
	sf    singleflight.Group
}

func NewHandlers(set *settings.Service, usageStore *usage.Store, users *user.Store, models *model.Store) *Handlers {
	return &Handlers{
		settings: set,
		usage:    usageStore,
		users:    users,
		models:   models,
		Limiter:  httpx.NewTokenBucketLimiter(2, 5),
		cache:    make(map[string]*cachedBoard),
	}
}

func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.Handle("GET /api/leaderboard", auth.RequireUser(httpx.Wrap(h.board)))
}

// Entry is one place on the accounts board, as a reader may see it.
type Entry struct {
	Rank int `json:"rank"`
	// Empty in anonymous mode for everybody but the reader, who is always
	// named: hiding somebody from themselves protects nobody.
	Name     string `json:"name,omitempty"`
	Handle   string `json:"handle,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	Value    int64  `json:"value"`
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
	Models   int64  `json:"models"`
	// Whether this place is the reader's, so the page can mark it without
	// being told who anybody is.
	Self bool `json:"self,omitempty"`
}

// ModelEntry is one place on the models board.
type ModelEntry struct {
	Rank     int    `json:"rank"`
	Name     string `json:"name"`
	Avatar   string `json:"avatar,omitempty"`
	Value    int64  `json:"value"`
	Users    int64  `json:"users"`
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
}

// Standing is where the reader is, whether or not that is on the list.
type Standing struct {
	Rank  int   `json:"rank"`
	Value int64 `json:"value"`
	// How far the place above is ahead. Zero at the top, and zero when the
	// reader has not used anything yet — Rank is zero then too.
	Gap          int64 `json:"gap"`
	Participants int   `json:"participants"`
}

func (h *Handlers) getCache(key string) *cachedBoard {
	h.mu.RLock()
	defer h.mu.RUnlock()
	entry, ok := h.cache[key]
	if !ok || time.Since(entry.createdAt) >= 30*time.Second {
		return nil
	}
	return entry
}

func (h *Handlers) setCache(key string, b *cachedBoard) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cache[key] = b
}

func (h *Handlers) board(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	if !account.CanAdmin(Permission) && !h.settings.Bool(settings.LeaderboardShowUsers) {
		return httpx.Forbidden("The leaderboard is not visible to users.")
	}

	clientIP := r.RemoteAddr
	if h.ClientIP != nil {
		clientIP = h.ClientIP(r)
	}
	rateKey := clientIP + ":" + account.ID
	if h.Limiter != nil && !h.Limiter.Allow(rateKey) {
		return httpx.TooManyRequests("rate_limited", "Too many leaderboard requests. Please try again later.")
	}

	query := r.URL.Query()
	period := query.Get("period")
	if period == "" {
		period = "week"
	}
	window, ok := periods[period]
	if !ok {
		return httpx.BadRequest("Unknown period %q.", period)
	}
	metricName := query.Get("metric")
	if metricName == "" {
		metricName = "tokens"
	}
	metric, ok := metrics[metricName]
	if !ok {
		return httpx.BadRequest("Unknown metric %q.", metricName)
	}

	size := h.settings.Int(settings.LeaderboardSize, 20)
	if size < 1 || size > settings.MaxLeaderboardSize {
		size = 20
	}
	identity := h.settings.Get(settings.LeaderboardIdentity)
	if !settings.ValidLeaderboardIdentity(identity) {
		identity = settings.LeaderboardNickname
	}

	cacheKey := period
	board := h.getCache(cacheKey)
	if board == nil {
		res, err, _ := h.sf.Do(cacheKey, func() (any, error) {
			if b := h.getCache(cacheKey); b != nil {
				return b, nil
			}

			// Context is detached from client disconnection and given a 5s deadline.
			queryCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer cancel()

			release, err := httpx.AcquireAggregationSlot(queryCtx)
			if err != nil {
				return nil, err
			}
			defer release()

			filter := usage.Filter{
				Since:  time.Now().Add(-window).UnixMilli(),
				Status: usage.StatusOK,
			}

			userRows, err := h.usage.GroupBy(queryCtx, "user", usage.MetricTokens, filter)
			if err != nil {
				return nil, err
			}

			maxFetch := settings.MaxLeaderboardSize
			if maxFetch < 1 || maxFetch > 50 {
				maxFetch = 50
			}

			// Pre-collect top user IDs by tokens and by requests so that whichever
			// metric the caller asks for, the top accounts have their profiles resolved.
			topUserKeys := make(map[string]struct{})
			tokensLimit := maxFetch
			if tokensLimit > len(userRows) {
				tokensLimit = len(userRows)
			}
			for i := 0; i < tokensLimit; i++ {
				topUserKeys[userRows[i].Key] = struct{}{}
			}

			requestsSorted := make([]usage.Breakdown, len(userRows))
			copy(requestsSorted, userRows)
			sort.SliceStable(requestsSorted, func(i, j int) bool {
				if requestsSorted[i].Requests != requestsSorted[j].Requests {
					return requestsSorted[i].Requests > requestsSorted[j].Requests
				}
				if requestsSorted[i].TotalTokens != requestsSorted[j].TotalTokens {
					return requestsSorted[i].TotalTokens > requestsSorted[j].TotalTokens
				}
				return requestsSorted[i].Key < requestsSorted[j].Key
			})
			requestsLimit := maxFetch
			if requestsLimit > len(requestsSorted) {
				requestsLimit = len(requestsSorted)
			}
			for i := 0; i < requestsLimit; i++ {
				topUserKeys[requestsSorted[i].Key] = struct{}{}
			}

			userInfos := make(map[string]user.User, len(topUserKeys))
			for uid := range topUserKeys {
				if person, err := h.users.ByID(queryCtx, nil, uid); err == nil {
					userInfos[uid] = person
				}
			}

			var modelRows []usage.Breakdown
			if h.settings.Bool(settings.LeaderboardShowModels) {
				modelRows, err = h.usage.GroupBy(queryCtx, "model", usage.MetricUsers, filter)
				if err != nil {
					return nil, err
				}
			}

			b := &cachedBoard{
				createdAt: time.Now(),
				userRows:  userRows,
				userInfos: userInfos,
				modelRows: modelRows,
			}
			h.setCache(cacheKey, b)
			return b, nil
		})
		if err != nil {
			var httpxErr *httpx.Error
			if errors.As(err, &httpxErr) {
				return httpxErr
			}
			return httpx.Internal(err)
		}
		board = res.(*cachedBoard)
	}

	accounts, standing := h.projectAccounts(board, account.ID, metric, size, identity)
	out := map[string]any{
		"period":   period,
		"metric":   metricName,
		"identity": identity,
		"accounts": accounts,
		"me":       standing,
	}
	if h.settings.Bool(settings.LeaderboardShowModels) {
		models, err := h.projectModels(r.Context(), board.modelRows, account, metric, size)
		if err != nil {
			return httpx.Internal(err)
		}
		out["models"] = models
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func valueOf(row usage.Breakdown, metric string) int64 {
	if metric == usage.MetricRequests {
		return row.Requests
	}
	return row.TotalTokens
}

func (h *Handlers) projectAccounts(b *cachedBoard, self, metric string, size int, identity string) ([]Entry, Standing) {
	sorted := make([]usage.Breakdown, len(b.userRows))
	copy(sorted, b.userRows)

	if metric == usage.MetricRequests {
		sort.SliceStable(sorted, func(i, j int) bool {
			if sorted[i].Requests != sorted[j].Requests {
				return sorted[i].Requests > sorted[j].Requests
			}
			if sorted[i].TotalTokens != sorted[j].TotalTokens {
				return sorted[i].TotalTokens > sorted[j].TotalTokens
			}
			return sorted[i].Key < sorted[j].Key
		})
	} else {
		sort.SliceStable(sorted, func(i, j int) bool {
			if sorted[i].TotalTokens != sorted[j].TotalTokens {
				return sorted[i].TotalTokens > sorted[j].TotalTokens
			}
			if sorted[i].Requests != sorted[j].Requests {
				return sorted[i].Requests > sorted[j].Requests
			}
			return sorted[i].Key < sorted[j].Key
		})
	}

	ranks := make([]int, len(sorted))
	for i, row := range sorted {
		if i > 0 && valueOf(row, metric) == valueOf(sorted[i-1], metric) {
			ranks[i] = ranks[i-1]
		} else {
			ranks[i] = i + 1
		}
	}

	standing := Standing{Participants: len(sorted)}
	for i, row := range sorted {
		if row.Key == self {
			standing.Rank = ranks[i]
			standing.Value = valueOf(row, metric)
			for j := i - 1; j >= 0; j-- {
				if above := valueOf(sorted[j], metric); above > standing.Value {
					standing.Gap = above - standing.Value
					break
				}
			}
			break
		}
	}

	shownCount := len(sorted)
	if shownCount > size {
		shownCount = size
	}
	entries := make([]Entry, 0, shownCount)
	for i := 0; i < shownCount; i++ {
		row := sorted[i]
		entry := Entry{
			Rank:     ranks[i],
			Value:    valueOf(row, metric),
			Requests: row.Requests,
			Tokens:   row.TotalTokens,
			Models:   row.Models,
			Self:     row.Key == self,
		}
		if person, ok := b.userInfos[row.Key]; ok {
			switch {
			case entry.Self, identity != settings.LeaderboardAnonymous:
				entry.Name = person.Nickname
				if entry.Name == "" {
					entry.Name = person.Username
				}
				entry.Avatar = person.Avatar
				if identity == settings.LeaderboardHandle || (entry.Self && person.Nickname != "") {
					entry.Handle = person.Username
				}
			}
		}
		entries = append(entries, entry)
	}
	return entries, standing
}

func (h *Handlers) projectModels(ctx context.Context, modelRows []usage.Breakdown, account user.User, metric string, size int) ([]ModelEntry, error) {
	visible, err := h.models.ListForUser(ctx, account.GroupID, account.IsAdmin())
	if err != nil {
		return nil, err
	}
	known := make(map[string]model.Model, len(visible))
	for _, m := range visible {
		known[m.ID] = m
	}

	kept := make([]usage.Breakdown, 0, len(modelRows))
	for _, row := range modelRows {
		if _, ok := known[row.Key]; ok {
			kept = append(kept, row)
		}
	}

	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Users != kept[j].Users {
			return kept[i].Users > kept[j].Users
		}
		return valueOf(kept[i], metric) > valueOf(kept[j], metric)
	})
	if len(kept) > size {
		kept = kept[:size]
	}

	entries := make([]ModelEntry, 0, len(kept))
	for i, row := range kept {
		m := known[row.Key]
		rank := i + 1
		if i > 0 && kept[i].Users == kept[i-1].Users && valueOf(kept[i], metric) == valueOf(kept[i-1], metric) {
			rank = entries[i-1].Rank
		}
		entries = append(entries, ModelEntry{
			Rank:     rank,
			Name:     m.DisplayName,
			Avatar:   m.Avatar,
			Value:    valueOf(row, metric),
			Users:    row.Users,
			Requests: row.Requests,
			Tokens:   row.TotalTokens,
		})
	}
	return entries, nil
}
