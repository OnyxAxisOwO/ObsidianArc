package admin

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Everyone's allowance at once: how much of each window every account has
// left. The per-account panel answers "why can this person not send anything";
// this answers "who is about to be unable to", which is a question about the
// whole population and has to be sorted across it — a page of the newest
// accounts says nothing about the ones closest to their ceiling.

// A listing this size is already more accounts than anyone reads one screen
// of. Past it the rows and the state counts cover only the newest accounts,
// so the response says it is capped and carries the real total, rather than
// presenting the partial figures as complete.
const allowanceAccountCap = 5000

// Shares of a window from which an account counts as running low.
const allowanceLow = 0.8

// How many allowance rankings the usage cache holds at once. Each one is up to
// allowanceAccountCap rows and every distinct search makes another, so the
// general bound on the map would let a burst of searches hold all of them for
// the whole 15-second window.
const allowanceRankingsCached = 4

// Rankings live in the usage summary's map under this prefix. The prefix is what
// forgetAllowanceRankings and the eviction in capAllowanceRankingsLocked use to
// tell a ranking from a summary, so every ranking key is built by
// allowanceRankingKey and never spelled out at a call site.
const allowanceRankingPrefix = "allowances:"

func allowanceRankingKey(groupID, search string) string {
	return allowanceRankingPrefix + groupID + ":" + search
}

type allowanceRow struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Nickname     string `json:"nickname"`
	GroupID      string `json:"group_id"`
	LastActiveAt int64  `json:"last_active_at"`
	// The tightest enforced window, 0-1; null where nothing constrains the
	// account, which sorts it last.
	Pressure  *float64            `json:"pressure"`
	Unlimited bool                `json:"unlimited"`
	Windows   []quota.WindowUsage `json:"windows"`
}

// rankedAllowances is every matching account, ranked, before the state filter
// and the page are taken. Building it reads every account and its usage, which
// is what each scroll of the page used to repeat, so it is kept briefly.
type rankedAllowances struct {
	rows      []allowanceRow
	matching  int
	capped    bool
	low       int
	exhausted int
}

func (h *Handlers) usageAllowances(w http.ResponseWriter, r *http.Request) error {
	if err := h.allowUsageRead(r); err != nil {
		return err
	}

	query := r.URL.Query()
	groupID := query.Get("group_id")
	if groupID != "" && !id.Valid(groupID) {
		return httpx.BadRequest("Malformed group id.")
	}
	state := query.Get("state")
	switch state {
	case "", "low", "exhausted":
	default:
		return httpx.BadRequest("Unknown state filter.")
	}

	ranked, err := h.rankedAllowances(r.Context(), query.Get("q"), groupID)
	if err != nil {
		return httpx.Internal(err)
	}

	// The low and exhausted counts describe the whole ranking, whichever state
	// is being listed; the state only narrows the rows the page is taken from.
	rows := ranked.rows
	if state != "" {
		rows = make([]allowanceRow, 0, len(ranked.rows))
		for _, row := range ranked.rows {
			if state == "low" && (row.Pressure == nil || *row.Pressure < allowanceLow) {
				continue
			}
			if state == "exhausted" && (row.Pressure == nil || *row.Pressure < 1) {
				continue
			}
			rows = append(rows, row)
		}
	}

	// listed is what the paging can reach; total is the real number of accounts
	// behind the list. A state filter can only be counted over the accounts
	// that were loaded, so past the cap it reports that count, and loaded says
	// how many accounts that count was drawn from.
	listed := len(rows)
	total := ranked.matching
	if state != "" {
		total = listed
	}
	limit := intParam(query.Get("limit"), 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := min(max(intParam(query.Get("offset"), 0), 0), listed)
	rows = rows[offset:min(offset+limit, listed)]

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"rows":      rows,
		"total":     total,
		"listed":    listed,
		"capped":    ranked.capped,
		"loaded":    len(ranked.rows),
		"display":   h.quota.Display(),
		"low":       ranked.low,
		"exhausted": ranked.exhausted,
	})
}

// rankedAllowances answers the ranking for one search and group from the cache
// when it is fresh. The cache is the usage summary's, with the same lifetime,
// and singleflight keeps a page scroll that arrives while the ranking is being
// built from starting a second build.
func (h *Handlers) rankedAllowances(ctx context.Context, search, groupID string) (rankedAllowances, error) {
	key := allowanceRankingKey(groupID, search)
	if ranked, ok := cachedRanking(h.getSummaryCache(key)); ok {
		return ranked, nil
	}
	// The generation is read before the build starts, and it is part of the
	// singleflight key. A request that comes after a write therefore never joins
	// a build begun before it, and a build that finishes after a write is not
	// cached, because the write may have changed what it read.
	generation := h.allowanceGenerationNow()
	res, err, _ := h.usageCache.allowanceSF.Do(strconv.FormatUint(generation, 10)+"|"+key, func() (any, error) {
		if ranked, ok := cachedRanking(h.getSummaryCache(key)); ok {
			return ranked, nil
		}
		// Detached from this request: the build is shared by everyone waiting on
		// it, so the viewer who started it leaving must not fail the rest.
		buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		ranked, err := h.buildAllowances(buildCtx, search, groupID)
		if err != nil {
			return nil, err
		}
		h.cacheRankingIfCurrent(key, ranked, generation)
		return ranked, nil
	})
	if err != nil {
		return rankedAllowances{}, err
	}
	return res.(rankedAllowances), nil
}

// forgetAllowanceRankings is called after a write that changes what an account
// has left: a reset, or a limit saved or removed. The ranking is read from the
// usage counters and the policies, so without this the list would show the
// pressure from before the write for as long as the cache holds it. It runs
// after the write has committed, so a rebuild cannot read the old figures back.
func (h *Handlers) forgetAllowanceRankings() {
	h.usageCache.mu.Lock()
	defer h.usageCache.mu.Unlock()
	h.usageCache.summary.deletePrefix(allowanceRankingPrefix)
	h.usageCache.allowanceGeneration++
}

func (h *Handlers) allowanceGenerationNow() uint64 {
	h.usageCache.mu.RLock()
	defer h.usageCache.mu.RUnlock()
	return h.usageCache.allowanceGeneration
}

// cacheRankingIfCurrent stores a ranking only when no write has come in since
// the build began. The check and the insert share the lock forgetAllowanceRankings
// takes, so a write cannot land between them and be followed by this stale copy.
func (h *Handlers) cacheRankingIfCurrent(key string, ranked rankedAllowances, generation uint64) {
	h.usageCache.mu.Lock()
	defer h.usageCache.mu.Unlock()
	if h.usageCache.allowanceGeneration != generation {
		return
	}
	h.putSummaryLocked(key, map[string]any{"ranked": ranked})
}

// capAllowanceRankingsLocked makes room for key among the cached rankings by
// dropping the oldest one whatever its age. It runs under usageCache.mu, from
// putSummaryLocked, and a key that is already cached is replaced rather than
// added, so it does not count against the cap.
func (h *Handlers) capAllowanceRankingsLocked(key string) {
	h.usageCache.summary.makeRoomUnder(allowanceRankingPrefix, key, allowanceRankingsCached)
}

func cachedRanking(cached map[string]any) (rankedAllowances, bool) {
	ranked, ok := cached["ranked"].(rankedAllowances)
	return ranked, ok
}

// buildAllowances reads every matching account, summarises each one's usage
// and sorts them by pressure. Nothing is filtered or paged here.
func (h *Handlers) buildAllowances(ctx context.Context, search, groupID string) (rankedAllowances, error) {
	filter := user.ListFilter{
		Search:  search,
		Status:  user.StatusActive,
		GroupID: groupID,
		Limit:   200,
	}
	var accounts []user.User
	var matching int
	for {
		page, total, err := h.users.List(ctx, filter)
		if err != nil {
			return rankedAllowances{}, err
		}
		matching = total
		accounts = append(accounts, page...)
		filter.Offset += len(page)
		if len(page) == 0 || filter.Offset >= total || len(accounts) >= allowanceAccountCap {
			break
		}
	}

	summaries, err := h.quota.SummariesFor(ctx, accounts)
	if err != nil {
		return rankedAllowances{}, err
	}

	ranked := rankedAllowances{
		rows:     make([]allowanceRow, 0, len(accounts)),
		matching: matching,
		capped:   matching > len(accounts),
	}
	for _, account := range accounts {
		summary := summaries[account.ID]
		row := allowanceRow{
			ID: account.ID, Username: account.Username, Nickname: account.Nickname,
			GroupID: account.GroupID, LastActiveAt: account.LastActiveAt,
			Pressure: allowancePressure(summary.Windows), Unlimited: summary.Unlimited, Windows: summary.Windows,
		}
		if row.Pressure != nil {
			if *row.Pressure >= 1 {
				ranked.exhausted++
			}
			if *row.Pressure >= allowanceLow {
				ranked.low++
			}
		}
		ranked.rows = append(ranked.rows, row)
	}

	sort.SliceStable(ranked.rows, func(i, j int) bool {
		a, b := ranked.rows[i].Pressure, ranked.rows[j].Pressure
		switch {
		case a == nil || b == nil:
			return a != nil && b == nil
		case *a != *b:
			return *a > *b
		}
		return ranked.rows[i].LastActiveAt > ranked.rows[j].LastActiveAt
	})
	return ranked, nil
}

// allowancePressure is how full the fullest enforced window is, judged on
// whichever of its limits is closest — the figure the account's own bars
// are drawn from.
func allowancePressure(windows []quota.WindowUsage) *float64 {
	var worst *float64
	note := func(used, limit float64) {
		if limit <= 0 {
			return
		}
		ratio := min(1, used/limit)
		if worst == nil || ratio > *worst {
			worst = &ratio
		}
	}
	for _, window := range windows {
		if !window.Enforced {
			continue
		}
		if window.LimitRequests != nil {
			note(float64(window.UsedRequests), float64(*window.LimitRequests))
		}
		if window.LimitTokens != nil {
			note(float64(window.UsedTokens), float64(*window.LimitTokens))
		}
		if window.LimitCredits != nil {
			note(window.UsedCredits, *window.LimitCredits)
		}
	}
	return worst
}
