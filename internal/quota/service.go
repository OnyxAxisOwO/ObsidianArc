package quota

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// ExceededError says which window stopped a request and when it frees up.
// The chat gateway turns it into a 429; the interface shows the reset time,
// which is the only actionable part.
type ExceededError struct {
	Window    Window
	Dimension string // "requests" | "tokens" | "credits"
	Used      float64
	Limit     float64
	ResetsAt  time.Time
}

func (e *ExceededError) Error() string {
	return fmt.Sprintf("quota: %s %s limit reached (%.0f of %.0f)",
		e.Window, e.Dimension, e.Used, e.Limit)
}

type Service struct {
	db       *database.DB
	policies *Store
	settings *settings.Service
	// The bonus bars spent beside the windows; nil means there are none.
	bonus *bonus.Store

	// Generations in flight per account. See Begin.
	mu       sync.Mutex
	inFlight map[string]int
}

func NewService(db *database.DB, policies *Store, set *settings.Service) *Service {
	return &Service{db: db, policies: policies, settings: set}
}

// SetBonus turns on spending from bonus bars. It is set once, at start.
func (s *Service) SetBonus(store *bonus.Store) { s.bonus = store }

// Policies returns the store, for the administration handlers.
func (s *Service) Policies() *Store { return s.policies }

// PolicyFor resolves the limits that apply to one account.
func (s *Service) PolicyFor(ctx context.Context, q database.Queryer, account user.User) (Policy, error) {
	if s.exempt(account) {
		return newPolicy(ScopeUser, account.ID), nil
	}

	layers, err := s.policies.layersFor(ctx, q, account.GroupID, account.ID)
	if err != nil {
		return Policy{}, err
	}
	return Resolve(layers...), nil
}

// Administrators are exempt by default, because an operator locked out of
// their own instance has no way back in. It is a setting rather than a
// constant so a shared deployment can turn it off.
func (s *Service) exempt(account user.User) bool {
	return account.IsAdmin() && s.settings.Bool(settings.AdminsBypassQuota)
}

// Estimate is the most a turn could cost. ReserveFor takes it up front and
// Settle gives back whatever was not used.
//
// Output only. What the prompt costs is not known until the transcript has
// been assembled, which happens after this; it is trued up at settle like
// everything else. Output is the term that can run away, and the term an
// attacker controls by asking for more.
type Estimate struct {
	Tokens  int64
	Credits float64
}

func (e Estimate) empty() bool { return e.Tokens <= 0 && e.Credits <= 0 }

// AutoReset spends whatever restores this account's allowance for the needed
// window. It receives the reservation transaction and the exceeded window,
// and reports which windows were reset (an empty slice means all allowance
// windows; the per-minute counters are never reset this way).
type AutoReset func(ctx context.Context, q database.Queryer, needed Window) (windows []string, spent bool, err error)

// Lock order. Every transaction takes the rows it needs in this order, and never
// a row that comes before one it already holds. Two transactions that take the
// same rows in opposite orders can each hold one and wait for the other, and
// PostgreSQL then aborts one of them with a deadlock (SQLSTATE 40P01); the
// request or turn that loses fails.
//
//  1. The global reset row in settings. A reservation that charges the allowance
//     windows takes it first, which puts it in line with the others and with
//     every reset. Settlements and releases never take it, so a reset does not
//     hold up a turn that is ending.
//  2. The account's users row, by a reservation that may spend a reset card.
//  3. The bonus_pools rows that a request is paid from, refunded to, or settled
//     against, each followed by the account's bonus_pool_usage row in it, one pool
//     at a time in the order they were made; then the bonus_grants rows.
//  4. The account's usage_counters rows, one window kind at a time, in the order
//     of counterKinds. A transaction takes at most one row of each kind.
//  5. The account's usage_cards rows, which a reset card spends. Their place is not
//     this list's: a reservation spends its card after the windows it walked, and a
//     manual use spends it before clearing them. That is safe because both spend
//     under the global reset row (1), and settlements and releases take no card
//     row, so a turn that is ending never waits on one. A plugin's claw-back
//     deletes unspent cards without row 1, under the acting administrator's row;
//     no path that holds a card row waits for that row, so it cannot close a cycle.
//
// The administrator resets are the exception to the last rule: they delete many
// rows of one kind. They hold the global reset row, so no two of them meet, and
// they delete one kind at a time in counterKinds order, so a delete never holds a
// row of one kind while it waits for a row of an earlier one.
//
// The prune of rolled-over counters is the other exception: it deletes old rows of
// every account and kind at once. It takes the global reset row before each chunk
// of at most pruneChunkRows, so it never meets a reset, and it lets go between
// chunks, so a reservation waiting on that row is not held behind the whole table.
var counterKinds = []Window{WindowRPM, WindowTPM, Window5H, WindowWeek, WindowMonth}

// tokenWindows are the counters a turn's tokens and credits land in: the
// minute, then every allowance window, which is the lock order above. Settle
// and Release walk this one list rather than spelling it out, because a window
// ReserveFor charges and one of them forgets would keep the estimate for ever.
var tokenWindows = append([]Window{WindowTPM}, AllowanceWindows...)

type counter struct {
	Requests int64
	Tokens   int64
	Credits  float64
}

// bump is the atomic increment. The RETURNING clause is what makes this one
// statement rather than a read followed by a write, and both SQLite (3.35+)
// and Postgres support it.
func bump(ctx context.Context, q database.Queryer, key string, window Window, start int64,
	requests, tokens int64, credits float64) (counter, error) {
	var out counter
	// The row a refund lands on may not be there any more: a reset card
	// deletes the account's counters while its turns are still in flight,
	// and each of them then releases into a bucket that has gone. The insert
	// therefore starts from the delta clamped at zero, and the deltas
	// themselves are passed again for the update; with excluded.* standing
	// in for both, a refund inserted a negative counter, and every card used
	// mid-turn handed out several turns' worth of free allowance.
	err := q.QueryRow(ctx,
		`INSERT INTO usage_counters (scope_key, window_kind, window_start, requests, tokens, credits)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (scope_key, window_kind, window_start) DO UPDATE SET
		   requests = usage_counters.requests + ?,
		   -- Refunds arrive here as negative deltas. Clamped, because two
		   -- settles racing on the same row must not leave a counter below
		   -- zero and hand out free allowance. CASE rather than GREATEST or
		   -- MAX: one of those is Postgres-only and the other is an aggregate
		   -- there.
		   tokens   = CASE WHEN usage_counters.tokens + ? < 0
		                   THEN 0 ELSE usage_counters.tokens + ? END,
		   credits  = CASE WHEN usage_counters.credits + ? < 0
		                   THEN 0 ELSE usage_counters.credits + ? END
		 RETURNING requests, tokens, credits`,
		key, window, start, max(requests, 0), max(tokens, 0), max(credits, 0),
		requests, tokens, tokens, credits, credits).
		Scan(&out.Requests, &out.Tokens, &out.Credits)
	if err != nil {
		return counter{}, fmt.Errorf("quota: bump %s: %w", window, err)
	}
	return out, nil
}

// The one place the per-account scope key is spelled, so ResetGroup can build
// the same key in SQL without the two drifting apart.
const userScopePrefix = "u:"

func scopeKey(userID string) string { return userScopePrefix + userID }

// AsExceeded extracts the typed rejection from a wrapped error.
func AsExceeded(err error) (*ExceededError, bool) {
	var exceeded *ExceededError
	if errors.As(err, &exceeded) {
		return exceeded, true
	}
	return nil, false
}
