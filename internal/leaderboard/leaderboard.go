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
	"strconv"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/flight"
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

// fallbackSize is the board's length when the stored size does not parse or is
// out of range. It matches the registered default for leaderboard.size, so a
// broken value draws the board an untouched instance would.
const fallbackSize = 20

// Periods a reader may choose, as how far back from now they reach. A fixed
// set rather than any duration a caller sends, so a request cannot ask for an
// aggregate over the whole ledger on every page load. A month here is thirty
// days, a rolling window; the quota's month is a calendar month anchored to the
// account, so the two do not line up and neither is wrong for its own page.
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
	// Both orders are fixed when the board is built. A read used to sort every
	// account again, once per request, while the cache held the board for all
	// of its readers.
	byTokens   []usage.Breakdown
	byRequests []usage.Breakdown
	userInfos  map[string]user.User
	modelRows  []usage.Breakdown
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
	sf    flight.Group
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
	// named: hiding somebody from themselves protects nobody. Also empty for an
	// account that never set a nickname, outside handle mode, because its login
	// is not published to other readers.
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
	// An IPv6 subscriber can present a new address per request inside one
	// /64, so the limit is kept per prefix, as every other per-address limit is.
	rateKey := httpx.RateKey(clientIP) + ":" + account.ID
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

	size := h.settings.Int(settings.LeaderboardSize, fallbackSize)
	if size < 1 || size > settings.MaxLeaderboardSize {
		size = fallbackSize
	}
	identity := h.settings.Get(settings.LeaderboardIdentity)
	if !settings.ValidLeaderboardIdentity(identity) {
		identity = settings.LeaderboardNickname
	}

	// The models board is fetched only when its setting is on at build time,
	// so a board cached with it off has no model rows. The flag is part of the
	// key, or switching the setting on would serve that empty board for the
	// rest of the 30-second TTL.
	showModels := h.settings.Bool(settings.LeaderboardShowModels)
	cacheKey := period + "|models=" + strconv.FormatBool(showModels)
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

			// The cache is shared by every reader and outlives a size change, so it
			// resolves the largest board an operator can set. A place past the
			// fetched profiles is shown as anonymous, which is wrong for a place the
			// board does show.
			maxFetch := settings.MaxLeaderboardSize

			// Pre-collect top user IDs by tokens and by requests so that whichever
			// metric the caller asks for, the top accounts have their profiles resolved.
			// They are read from the orders the board is drawn from: a place drawn
			// without its profile is shown as anonymous.
			byTokens, byRequests := orderAccounts(userRows)
			topUserKeys := make(map[string]struct{})
			for _, order := range [][]usage.Breakdown{byTokens, byRequests} {
				limit := maxFetch
				if limit > len(order) {
					limit = len(order)
				}
				for i := 0; i < limit; i++ {
					topUserKeys[order[i].Key] = struct{}{}
				}
			}

			// One query for up to two hundred profiles; reading them one at a time
			// was two hundred round trips on every rebuild.
			ids := make([]string, 0, len(topUserKeys))
			for uid := range topUserKeys {
				ids = append(ids, uid)
			}
			userInfos, err := h.users.ProfilesByID(queryCtx, nil, ids)
			if err != nil {
				return nil, err
			}

			var modelRows []usage.Breakdown
			if showModels {
				modelRows, err = h.usage.GroupBy(queryCtx, "model", usage.MetricUsers, filter)
				if err != nil {
					return nil, err
				}
			}

			b := &cachedBoard{
				createdAt:  time.Now(),
				byTokens:   byTokens,
				byRequests: byRequests,
				userInfos:  userInfos,
				modelRows:  modelRows,
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
	if showModels {
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

// sortByRequests is the requests board's order. The profiles collected for the
// cached board and the board drawn from it must agree on who is in the top N,
// or a ranked account is drawn without its profile, so both sort through here.
func sortByRequests(rows []usage.Breakdown) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Requests != rows[j].Requests {
			return rows[i].Requests > rows[j].Requests
		}
		if rows[i].TotalTokens != rows[j].TotalTokens {
			return rows[i].TotalTokens > rows[j].TotalTokens
		}
		return rows[i].Key < rows[j].Key
	})
}

// sortByTokens is the tokens board's order, with the same tie-breaks as
// sortByRequests so that the two boards rank a tie the same way.
func sortByTokens(rows []usage.Breakdown) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].TotalTokens != rows[j].TotalTokens {
			return rows[i].TotalTokens > rows[j].TotalTokens
		}
		if rows[i].Requests != rows[j].Requests {
			return rows[i].Requests > rows[j].Requests
		}
		return rows[i].Key < rows[j].Key
	})
}

// orderAccounts returns the accounts in the tokens order and in the requests
// order. Each is a copy, so the fetched rows keep their own order.
func orderAccounts(rows []usage.Breakdown) (byTokens, byRequests []usage.Breakdown) {
	byTokens = make([]usage.Breakdown, len(rows))
	copy(byTokens, rows)
	sortByTokens(byTokens)
	byRequests = make([]usage.Breakdown, len(rows))
	copy(byRequests, rows)
	sortByRequests(byRequests)
	return byTokens, byRequests
}

func (h *Handlers) projectAccounts(b *cachedBoard, self, metric string, size int, identity string) ([]Entry, Standing) {
	// Read-only: the cached orders are shared by every reader of the board.
	sorted := b.byTokens
	if metric == usage.MetricRequests {
		sorted = b.byRequests
	}

	ranks := make([]int, len(sorted))
	for i, row := range sorted {
		if i > 0 && valueOf(row, metric) == valueOf(sorted[i-1], metric) {
			ranks[i] = ranks[i-1]
		} else {
			ranks[i] = i + 1
		}
	}

	shownCount := len(sorted)
	if shownCount > size {
		shownCount = size
	}

	standing := Standing{Participants: len(sorted)}
	for i, row := range sorted {
		if row.Key == self {
			standing.Rank = ranks[i]
			standing.Value = valueOf(row, metric)
			for j := i - 1; j >= 0; j-- {
				if above := valueOf(sorted[j], metric); above > standing.Value {
					// The gap is the place above's total less the reader's own. Only a
					// place on the board may be measured: one past its end would reveal
					// that unlisted account's total, which the board never publishes.
					if j < shownCount {
						standing.Gap = above - standing.Value
					}
					break
				}
			}
			break
		}
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
				// A login name is half of a login credential, so another reader sees it
				// only in handle mode, where the operator chose to show it. The reader's
				// own login is theirs already. Any other account with no nickname is left
				// unnamed, and the page shows that as an anonymous place.
				if entry.Name == "" && (entry.Self || identity == settings.LeaderboardHandle) {
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

	// Ranked by the metric before the cut. Cutting by distinct users first kept
	// the wrong models: the one with the most tokens but few users was dropped
	// before the page could sort by tokens.
	sort.SliceStable(kept, func(i, j int) bool {
		if vi, vj := valueOf(kept[i], metric), valueOf(kept[j], metric); vi != vj {
			return vi > vj
		}
		return kept[i].Users > kept[j].Users
	})
	if len(kept) > size {
		kept = kept[:size]
	}

	entries := make([]ModelEntry, 0, len(kept))
	for i, row := range kept {
		m := known[row.Key]
		// Places are shared on the metric alone, as on the accounts board: models
		// level on the figure share a place, so the figure beside a name never
		// implies a lead that the figure beside the other name does not show.
		// Users only orders the models that are level.
		rank := i + 1
		if i > 0 && valueOf(kept[i], metric) == valueOf(kept[i-1], metric) {
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
