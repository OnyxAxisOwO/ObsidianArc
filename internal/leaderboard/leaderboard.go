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
	"net/http"
	"sort"
	"time"

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

type Handlers struct {
	settings *settings.Service
	usage    *usage.Store
	users    *user.Store
	models   *model.Store
}

func NewHandlers(set *settings.Service, usageStore *usage.Store, users *user.Store, models *model.Store) *Handlers {
	return &Handlers{settings: set, usage: usageStore, users: users, models: models}
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

func (h *Handlers) board(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	if !account.CanAdmin(Permission) && !h.settings.Bool(settings.LeaderboardShowUsers) {
		return httpx.Forbidden("The leaderboard is not visible to users.")
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

	filter := usage.Filter{
		Since:  time.Now().Add(-window).UnixMilli(),
		Status: usage.StatusOK,
	}

	accounts, standing, err := h.accounts(r.Context(), account.ID, metric, filter, size, identity)
	if err != nil {
		return httpx.Internal(err)
	}

	out := map[string]any{
		"period":   period,
		"metric":   metricName,
		"identity": identity,
		"accounts": accounts,
		"me":       standing,
	}
	if h.settings.Bool(settings.LeaderboardShowModels) {
		models, err := h.modelBoard(r.Context(), account, metric, filter, size)
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

// accounts builds the accounts board and the reader's standing on it.
//
// The whole ranking is read — GroupBy returns every group — because the
// reader's place has to be right when it is two hundredth, not only when it
// is in the part that is shown. What leaves the server is only the top of it.
func (h *Handlers) accounts(ctx context.Context, self, metric string, filter usage.Filter, size int, identity string) ([]Entry, Standing, error) {
	rows, err := h.usage.GroupBy(ctx, "user", metric, filter)
	if err != nil {
		return nil, Standing{}, err
	}

	// GroupBy orders by the metric and breaks ties by request count, then id.
	// Places are then given competition-style — two people level on tokens
	// share a place — so the number beside a name never implies a lead that
	// is not in the figure beside it.
	standing := Standing{Participants: len(rows)}
	ranks := make([]int, len(rows))
	for i, row := range rows {
		if i > 0 && valueOf(row, metric) == valueOf(rows[i-1], metric) {
			ranks[i] = ranks[i-1]
		} else {
			ranks[i] = i + 1
		}
		if row.Key == self {
			standing.Rank = ranks[i]
			standing.Value = valueOf(row, metric)
			// The nearest figure above that is actually higher, so a tie at
			// the top does not report a gap of nought to overtake.
			for j := i - 1; j >= 0; j-- {
				if above := valueOf(rows[j], metric); above > standing.Value {
					standing.Gap = above - standing.Value
					break
				}
			}
		}
	}

	shown := rows
	if len(shown) > size {
		shown = shown[:size]
	}
	entries := make([]Entry, 0, len(shown))
	for i, row := range shown {
		entry := Entry{
			Rank:     ranks[i],
			Value:    valueOf(row, metric),
			Requests: row.Requests,
			Tokens:   row.TotalTokens,
			Models:   row.Models,
			Self:     row.Key == self,
		}
		// Looked up rather than taken from the breakdown: the breakdown's
		// label falls back to the account id when there is no name, and an
		// id is the one thing this board does not hand out. A place whose
		// account has since been deleted keeps its figures and loses its name.
		if person, err := h.users.ByID(ctx, nil, row.Key); err == nil {
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
	return entries, standing, nil
}

// modelBoard ranks models by how many people use them, then by the metric the
// reader chose. Popularity rather than volume, because "what does everybody
// reach for" is the question a reader has about models; one account's heavy
// use of something obscure answers a different one.
//
// Only models the reader's group can see are listed, under their current
// names, so the board never announces a model the reader has no way to open.
func (h *Handlers) modelBoard(ctx context.Context, account user.User, metric string, filter usage.Filter, size int) ([]ModelEntry, error) {
	visible, err := h.models.ListForUser(ctx, account.GroupID, account.IsAdmin())
	if err != nil {
		return nil, err
	}
	known := make(map[string]model.Model, len(visible))
	for _, m := range visible {
		known[m.ID] = m
	}

	rows, err := h.usage.GroupBy(ctx, "model", usage.MetricUsers, filter)
	if err != nil {
		return nil, err
	}
	kept := make([]usage.Breakdown, 0, len(rows))
	for _, row := range rows {
		if _, ok := known[row.Key]; ok {
			kept = append(kept, row)
		}
	}
	// Stable, so rows already in the ledger's order keep it when both figures
	// are level.
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
