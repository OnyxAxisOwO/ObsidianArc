package admin

import (
	"net/http"
	"sort"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Everyone's allowance at once: how much of each window every account has
// left. The per-account panel answers "why can this person not send anything";
// this answers "who is about to be unable to", which is a question about the
// whole population and has to be sorted across it — a page of the newest
// accounts says nothing about the ones closest to their ceiling.

// A listing this size is already more accounts than anyone reads one screen
// of; past it the page says how many there are rather than loading them all.
const allowanceAccountCap = 5000

// Shares of a window from which an account counts as running low.
const allowanceLow = 0.8

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

func (h *Handlers) usageAllowances(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	clientIP := r.RemoteAddr
	if h.ClientIP != nil {
		clientIP = h.ClientIP(r)
	}
	if h.UsageLimiter != nil && !h.UsageLimiter.Allow(clientIP+":"+actor.ID) {
		return httpx.TooManyRequests("rate_limited", "Too many usage requests. Please try again later.")
	}

	query := r.URL.Query()
	filter := user.ListFilter{
		Search:  query.Get("q"),
		Status:  user.StatusActive,
		GroupID: query.Get("group_id"),
		Limit:   200,
	}
	if filter.GroupID != "" && !isValidID(filter.GroupID) {
		return httpx.BadRequest("Malformed group id.")
	}
	state := query.Get("state")
	switch state {
	case "", "low", "exhausted":
	default:
		return httpx.BadRequest("Unknown state filter.")
	}

	var accounts []user.User
	for {
		page, total, err := h.users.List(r.Context(), filter)
		if err != nil {
			return httpx.Internal(err)
		}
		accounts = append(accounts, page...)
		filter.Offset += len(page)
		if len(page) == 0 || filter.Offset >= total || len(accounts) >= allowanceAccountCap {
			break
		}
	}

	summaries, err := h.quota.SummariesFor(r.Context(), accounts)
	if err != nil {
		return httpx.Internal(err)
	}

	rows := make([]allowanceRow, 0, len(accounts))
	var low, exhausted int
	for _, account := range accounts {
		summary := summaries[account.ID]
		row := allowanceRow{
			ID: account.ID, Username: account.Username, Nickname: account.Nickname,
			GroupID: account.GroupID, LastActiveAt: account.LastActiveAt,
			Pressure: allowancePressure(summary.Windows), Unlimited: summary.Unlimited, Windows: summary.Windows,
		}
		if row.Pressure != nil {
			if *row.Pressure >= 1 {
				exhausted++
			}
			if *row.Pressure >= allowanceLow {
				low++
			}
		}
		switch state {
		case "low":
			if row.Pressure == nil || *row.Pressure < allowanceLow {
				continue
			}
		case "exhausted":
			if row.Pressure == nil || *row.Pressure < 1 {
				continue
			}
		}
		rows = append(rows, row)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].Pressure, rows[j].Pressure
		switch {
		case a == nil || b == nil:
			return a != nil && b == nil
		case *a != *b:
			return *a > *b
		}
		return rows[i].LastActiveAt > rows[j].LastActiveAt
	})

	total := len(rows)
	limit := intParam(query.Get("limit"), 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := min(intParam(query.Get("offset"), 0), total)
	rows = rows[offset:min(offset+limit, total)]

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"rows":      rows,
		"total":     total,
		"display":   h.quota.Display(),
		"low":       low,
		"exhausted": exhausted,
	})
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
