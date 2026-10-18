package quota

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// WindowUsage is one window as the interface shows it.
type WindowUsage struct {
	Kind     Window `json:"kind"`
	Enforced bool   `json:"enforced"`

	UsedRequests int64   `json:"used_requests"`
	UsedTokens   int64   `json:"used_tokens"`
	UsedCredits  float64 `json:"used_credits"`

	LimitRequests *int64   `json:"limit_requests"`
	LimitTokens   *int64   `json:"limit_tokens"`
	LimitCredits  *float64 `json:"limit_credits"`

	ResetsAt int64 `json:"resets_at"`
}

type Summary struct {
	Unlimited bool `json:"unlimited"`
	// How the instance wants these figures phrased: the raw pair, what is
	// left, or what has gone. Carried on the summary rather than fetched
	// separately, because it is only ever read alongside the numbers it
	// describes.
	Display string        `json:"display"`
	Windows []WindowUsage `json:"windows"`
}

// Display is how the instance phrases an allowance, for a caller that is
// handing summaries on without the Summary that normally carries it.
func (s *Service) Display() string { return s.settings.UsageDisplay() }

// SummaryFor is what the composer menu reads. It reports every allowance
// window, enforced or not, so a user can see their consumption on a server
// that has set no limits.
func (s *Service) SummaryFor(ctx context.Context, account user.User) (Summary, error) {
	policy, err := s.PolicyFor(ctx, nil, account)
	if err != nil {
		return Summary{}, err
	}

	now := time.Now()
	summary := Summary{
		Unlimited: policy.Unlimited(),
		Display:   s.settings.UsageDisplay(),
		Windows:   make([]WindowUsage, 0, len(AllowanceWindows)),
	}
	anchor, err := allowanceAnchor(ctx, s.db, account.CreatedAt)
	if err != nil {
		return Summary{}, err
	}

	for _, window := range AllowanceWindows {
		limits := policy.Windows[window]
		start := bucketStart(window, now, anchor)

		var used counter
		err := s.db.QueryRow(ctx,
			`SELECT requests, tokens, credits FROM usage_counters
			 WHERE scope_key = ? AND window_kind = ? AND window_start = ?`,
			scopeKey(account.ID), window, start).
			Scan(&used.Requests, &used.Tokens, &used.Credits)
		if err != nil && !database.IsNotFound(err) {
			return Summary{}, fmt.Errorf("quota: read counter: %w", err)
		}

		summary.Windows = append(summary.Windows, windowUsage(window, limits, used, now, anchor))
	}
	return summary, nil
}

func windowUsage(window Window, limits Limits, used counter, now time.Time, anchor int64) WindowUsage {
	return WindowUsage{
		Kind:          window,
		Enforced:      limits.isOn(),
		UsedRequests:  used.Requests,
		UsedTokens:    used.Tokens,
		UsedCredits:   round(used.Credits),
		LimitRequests: limits.Requests,
		LimitTokens:   limits.Tokens,
		LimitCredits:  limits.Credits,
		ResetsAt:      bucketEnd(window, now, anchor).UnixMilli(),
	}
}

// counterSlot is one row of usage_counters, by its primary key.
type counterSlot struct {
	scope  string
	window Window
	start  int64
}

// summaryKeyChunk bounds how many accounts one counter query names. It keeps
// the placeholder count well under what either engine accepts.
const summaryKeyChunk = 500

// summaryFloor is how far back the current bucket of each allowance window can
// start. A bucket begins at or before now and within one period of it, so a
// row older than this is never the bucket any account is charged in. A month
// is at most 31 days, and 32 leaves a day of slack.
var summaryFloor = map[Window]time.Duration{
	Window5H:    5 * time.Hour,
	WindowWeek:  7 * 24 * time.Hour,
	WindowMonth: 32 * 24 * time.Hour,
}

// readCurrentCounters loads the current bucket of each allowance window for a
// chunk of accounts into counters. Naming the accounts' scope keys is what
// bounds the read: scope_key leads the primary key, so the lookup uses it
// rather than scanning every account's history. The per-window floor bounds the
// read to rows that could be an account's current bucket. For the month window
// it can include the previous month's bucket too, which SummariesFor never
// looks up.
func readCurrentCounters(
	ctx context.Context, db *database.DB, accounts []user.User, now time.Time, counters map[counterSlot]counter,
) error {
	args := make([]any, 0, len(accounts)+2*len(AllowanceWindows))
	for _, account := range accounts {
		args = append(args, scopeKey(account.ID))
	}
	kinds := make([]string, 0, len(AllowanceWindows))
	for _, window := range AllowanceWindows {
		kinds = append(kinds, `(window_kind = ? AND window_start >= ?)`)
		args = append(args, window, now.Add(-summaryFloor[window]).UnixMilli())
	}

	rows, err := db.Query(ctx,
		`SELECT scope_key, window_kind, window_start, requests, tokens, credits FROM usage_counters
		 WHERE scope_key IN (`+database.Placeholders(len(accounts))+`) AND (`+strings.Join(kinds, " OR ")+`)`,
		args...)
	if err != nil {
		return fmt.Errorf("quota: read counters: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key counterSlot
		var used counter
		if err := rows.Scan(&key.scope, &key.window, &key.start, &used.Requests, &used.Tokens, &used.Credits); err != nil {
			return fmt.Errorf("quota: scan counter: %w", err)
		}
		counters[key] = used
	}
	return rows.Err()
}

// SummariesFor is SummaryFor for a whole list of accounts, in a few queries
// instead of a few per account: the policies, the global reset, and the
// current counters of each chunk of accounts. It is what an administrator's
// overview of everyone's allowance reads, where one query per account per
// window would make the page's cost grow with the instance.
//
// The answers are the ones SummaryFor gives each account. The stacking of
// levels is accountLayers, which PolicyFor shares, and the bucket arithmetic is
// bucketStart, so the two readers cannot disagree about which levels count. A
// counter for a window that has since rolled over is simply not found, which
// reads as nothing spent, exactly as it does there.
func (s *Service) SummariesFor(ctx context.Context, accounts []user.User) (map[string]Summary, error) {
	policies, err := s.policies.List(ctx)
	if err != nil {
		return nil, err
	}
	var global Policy
	groups := map[string]Policy{}
	users := map[string]Policy{}
	for _, policy := range policies {
		switch policy.Scope {
		case ScopeGlobal:
			global = policy
		case ScopeGroup:
			groups[policy.ScopeID] = policy
		case ScopeUser:
			users[policy.ScopeID] = policy
		}
	}
	resetAt, err := globalResetAt(ctx, s.db)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	counters := map[counterSlot]counter{}
	for from := 0; from < len(accounts); from += summaryKeyChunk {
		to := min(from+summaryKeyChunk, len(accounts))
		if err := readCurrentCounters(ctx, s.db, accounts[from:to], now, counters); err != nil {
			return nil, err
		}
	}

	display := s.settings.UsageDisplay()
	out := make(map[string]Summary, len(accounts))
	for _, account := range accounts {
		policy := newPolicy(ScopeUser, account.ID)
		if !s.exempt(account) {
			// A level with no row is the zero Policy here, which Resolve passes
			// over, so the account's own level is always stacked.
			group, own := groups[account.GroupID], users[account.ID]
			policy = Resolve(accountLayers(account.GroupID != "", &global, &group, &own)...)
		}
		anchor := anchorFor(account.CreatedAt, resetAt)
		summary := Summary{Unlimited: policy.Unlimited(), Display: display, Windows: make([]WindowUsage, 0, len(AllowanceWindows))}
		for _, window := range AllowanceWindows {
			used := counters[counterSlot{scopeKey(account.ID), window, bucketStart(window, now, anchor)}]
			summary.Windows = append(summary.Windows, windowUsage(window, policy.Windows[window], used, now, anchor))
		}
		out[account.ID] = summary
	}
	return out, nil
}

// PruneCounters drops buckets that have rolled over, each kind on its own
// horizon. Keeping every kind for the month window's two months kept a row per
// account per minute for the rate limits, millions of rows that nothing reads
// once their minute has passed.
//
// Each chunk takes the global reset row before its first delete, as the lock order
// requires. Without that the prune could meet an administrator's reset, which
// deletes these rows one kind at a time, and PostgreSQL would abort one of them.
// The chunks keep the row held briefly, because every reservation waits on it.
func (s *Service) PruneCounters(ctx context.Context) (int64, error) {
	now := time.Now()
	var removed int64
	for {
		n, err := s.pruneChunk(ctx, now)
		removed += n
		if err != nil {
			return removed, fmt.Errorf("quota: prune counters: %w", err)
		}
		if n < pruneChunkRows {
			return removed, nil
		}
	}
}

// counterRetention is how long a bucket of each kind is kept after it starts.
// Each is far past the last moment anything writes to that bucket: a release
// refunds into the bucket its reservation was taken from, which is one turn
// back, and a month bucket starts at most 31 days back. The margin is what
// keeps the prune's lock-order argument below true.
var counterRetention = map[Window]time.Duration{
	WindowRPM:   24 * time.Hour,
	WindowTPM:   24 * time.Hour,
	Window5H:    2 * 24 * time.Hour,
	WindowWeek:  60 * 24 * time.Hour,
	WindowMonth: 60 * 24 * time.Hour,
}

// pruneChunkRows bounds one prune transaction. The rows are taken oldest first,
// so a chunk that comes back short has reached the end of the stale rows.
const pruneChunkRows = 1000

// The chunk deletes rows of every account and kind in scan order, not in
// counterKinds order. That is safe because every row it can reach starts before
// its kind's horizon, and no reservation or settlement locks a row that old. Each
// locks the current bucket of a window, which starts at most one window back: a
// minute, five hours, a week, a month, each well inside its horizon. A release
// refunds into the bucket its reservation came out of, which is a turn's length
// back. Resets cannot meet it on these rows either,
// as they take the same global row first.
func (s *Service) pruneChunk(ctx context.Context, now time.Time) (int64, error) {
	stale := make([]string, 0, len(counterKinds))
	args := make([]any, 0, 2*len(counterKinds)+1)
	for _, kind := range counterKinds {
		stale = append(stale, `(window_kind = ? AND window_start < ?)`)
		args = append(args, kind, now.Add(-counterRetention[kind]).UnixMilli())
	}
	args = append(args, pruneChunkRows)

	var removed int64
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockAllowanceBoundary(ctx, tx); err != nil {
			return err
		}
		// The subquery names the rows, because DELETE ... LIMIT is not portable.
		result, err := tx.Exec(ctx,
			`DELETE FROM usage_counters WHERE (scope_key, window_kind, window_start) IN (
			   SELECT scope_key, window_kind, window_start FROM usage_counters
			   WHERE `+strings.Join(stale, " OR ")+` ORDER BY window_start LIMIT ?)`,
			args...)
		if err != nil {
			return err
		}
		removed, err = result.RowsAffected()
		return err
	})
	return removed, err
}
