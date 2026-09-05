package quota

import (
	"context"
	"errors"
	"fmt"
	"time"

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
}

func NewService(db *database.DB, policies *Store, set *settings.Service) *Service {
	return &Service{db: db, policies: policies, settings: set}
}

// Policies returns the store, for the administration handlers.
func (s *Service) Policies() *Store { return s.policies }

// PolicyFor resolves the limits that apply to one account.
func (s *Service) PolicyFor(ctx context.Context, q database.Queryer, account user.User) (Policy, error) {
	if s.exempt(account) {
		return newPolicy(ScopeUser, account.ID), nil
	}

	global, err := s.policies.GetOrEmpty(ctx, q, ScopeGlobal, "")
	if err != nil {
		return Policy{}, err
	}

	layers := []Policy{global}
	if account.GroupID != "" {
		groupPolicy, err := s.policies.GetOrEmpty(ctx, q, ScopeGroup, account.GroupID)
		if err != nil {
			return Policy{}, err
		}
		layers = append(layers, groupPolicy)
	}
	userPolicy, err := s.policies.GetOrEmpty(ctx, q, ScopeUser, account.ID)
	if err != nil {
		return Policy{}, err
	}
	return Resolve(append(layers, userPolicy)...), nil
}

// Administrators are exempt by default, because an operator locked out of
// their own instance has no way back in. It is a setting rather than a
// constant so a shared deployment can turn it off.
func (s *Service) exempt(account user.User) bool {
	return account.IsAdmin() && s.settings.Bool(settings.AdminsBypassQuota)
}

// Reserve claims one request against every window that applies, and fails if
// any of them is already spent.
//
// The whole thing runs in one transaction. Each window's counter is
// incremented and read back in a single statement, so the check sees a value
// no concurrent request can have moved underneath it; if any window is over,
// the transaction rolls back and every increment goes with it.
//
// Token and credit ceilings are checked against what has already been spent
// rather than predicted: the cost of a turn is not knowable until it is over,
// and refusing to start when the allowance is already gone is the honest
// approximation.
func (s *Service) Reserve(ctx context.Context, account user.User) error {
	policy, err := s.PolicyFor(ctx, nil, account)
	if err != nil {
		return err
	}
	if policy.Unlimited() {
		return nil
	}

	now := time.Now()
	key := scopeKey(account.ID)

	return s.db.Tx(ctx, func(tx *database.Tx) error {
		if policy.RPM != nil && *policy.RPM > 0 {
			counter, err := bump(ctx, tx, key, WindowRPM, bucketStart(WindowRPM, now), 1, 0, 0)
			if err != nil {
				return err
			}
			if counter.Requests > *policy.RPM {
				return &ExceededError{
					Window: WindowRPM, Dimension: "requests",
					Used: float64(counter.Requests), Limit: float64(*policy.RPM),
					ResetsAt: bucketEnd(WindowRPM, now),
				}
			}
		}

		if policy.TPM != nil && *policy.TPM > 0 {
			counter, err := bump(ctx, tx, key, WindowTPM, bucketStart(WindowTPM, now), 0, 0, 0)
			if err != nil {
				return err
			}
			if counter.Tokens >= *policy.TPM {
				return &ExceededError{
					Window: WindowTPM, Dimension: "tokens",
					Used: float64(counter.Tokens), Limit: float64(*policy.TPM),
					ResetsAt: bucketEnd(WindowTPM, now),
				}
			}
		}

		for _, window := range AllowanceWindows {
			limits := policy.Windows[window]
			if !limits.isOn() {
				continue
			}

			start := bucketStart(window, now)
			counter, err := bump(ctx, tx, key, window, start, 1, 0, 0)
			if err != nil {
				return err
			}
			resets := bucketEnd(window, now)

			if limits.Requests != nil && *limits.Requests > 0 && counter.Requests > *limits.Requests {
				return &ExceededError{
					Window: window, Dimension: "requests",
					Used: float64(counter.Requests), Limit: float64(*limits.Requests), ResetsAt: resets,
				}
			}
			// Already-spent comparisons: this request has not cost anything
			// yet, so exceeding is "there was nothing left before you asked".
			if limits.Tokens != nil && *limits.Tokens > 0 && counter.Tokens >= *limits.Tokens {
				return &ExceededError{
					Window: window, Dimension: "tokens",
					Used: float64(counter.Tokens), Limit: float64(*limits.Tokens), ResetsAt: resets,
				}
			}
			if limits.Credits != nil && *limits.Credits > 0 && counter.Credits >= *limits.Credits {
				return &ExceededError{
					Window: window, Dimension: "credits",
					Used: counter.Credits, Limit: *limits.Credits, ResetsAt: resets,
				}
			}
		}
		return nil
	})
}

// Settle adds what a finished turn actually consumed. It runs after the
// answer, on a detached context, so a cancelled turn is still accounted for.
//
// It touches every window unconditionally rather than only the enforced ones:
// the counters are also what the usage display reads, and a limit turned on
// tomorrow should not start from zero for someone who has been using the
// server all week.
func (s *Service) Settle(ctx context.Context, userID string, tokens int64, credits float64) error {
	if tokens <= 0 && credits <= 0 {
		return nil
	}
	now := time.Now()
	key := scopeKey(userID)

	return s.db.Tx(ctx, func(tx *database.Tx) error {
		for _, window := range []Window{WindowTPM, Window5H, WindowWeek, WindowMonth} {
			if _, err := bump(ctx, tx, key, window, bucketStart(window, now), 0, tokens, credits); err != nil {
				return err
			}
		}
		return nil
	})
}

// RecordRejection counts a refused request against the rate window only, so a
// client retrying in a loop still runs into the rate limit rather than being
// free to hammer the endpoint.
func (s *Service) RecordRejection(ctx context.Context, userID string) {
	now := time.Now()
	_, _ = bump(ctx, s.db, scopeKey(userID), WindowRPM, bucketStart(WindowRPM, now), 1, 0, 0)
}

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
	err := q.QueryRow(ctx,
		`INSERT INTO usage_counters (scope_key, window_kind, window_start, requests, tokens, credits)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (scope_key, window_kind, window_start) DO UPDATE SET
		   requests = usage_counters.requests + excluded.requests,
		   tokens   = usage_counters.tokens   + excluded.tokens,
		   credits  = usage_counters.credits  + excluded.credits
		 RETURNING requests, tokens, credits`,
		key, window, start, requests, tokens, credits).
		Scan(&out.Requests, &out.Tokens, &out.Credits)
	if err != nil {
		return counter{}, fmt.Errorf("quota: bump %s: %w", window, err)
	}
	return out, nil
}

// --- reporting ---------------------------------------------------------------

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

// SummaryFor is what the composer menu reads. It reports every allowance
// window, enforced or not, so a user can see their consumption on a server
// that has set no limits.
func (s *Service) SummaryFor(ctx context.Context, account user.User) (Summary, error) {
	policy, err := s.PolicyFor(ctx, nil, account)
	if err != nil {
		return Summary{}, err
	}

	now := time.Now()
	display := s.settings.Get(settings.UsageDisplay)
	if !settings.ValidUsageDisplay(display) {
		display = settings.UsageAbsolute
	}
	summary := Summary{
		Unlimited: policy.Unlimited(),
		Display:   display,
		Windows:   make([]WindowUsage, 0, len(AllowanceWindows)),
	}

	for _, window := range AllowanceWindows {
		limits := policy.Windows[window]
		start := bucketStart(window, now)

		var used counter
		err := s.db.QueryRow(ctx,
			`SELECT requests, tokens, credits FROM usage_counters
			 WHERE scope_key = ? AND window_kind = ? AND window_start = ?`,
			scopeKey(account.ID), window, start).
			Scan(&used.Requests, &used.Tokens, &used.Credits)
		if err != nil && !database.IsNotFound(err) {
			return Summary{}, fmt.Errorf("quota: read counter: %w", err)
		}

		summary.Windows = append(summary.Windows, WindowUsage{
			Kind:          window,
			Enforced:      limits.isOn(),
			UsedRequests:  used.Requests,
			UsedTokens:    used.Tokens,
			UsedCredits:   round(used.Credits),
			LimitRequests: limits.Requests,
			LimitTokens:   limits.Tokens,
			LimitCredits:  limits.Credits,
			ResetsAt:      bucketEnd(window, now).UnixMilli(),
		})
	}
	return summary, nil
}

// PruneCounters drops buckets that have rolled over. Monthly buckets are the
// longest-lived, so anything older than two months is certainly dead.
func (s *Service) PruneCounters(ctx context.Context) (int64, error) {
	cutoff := time.Now().AddDate(0, -2, 0).UnixMilli()
	result, err := s.db.Exec(ctx, `DELETE FROM usage_counters WHERE window_start < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("quota: prune counters: %w", err)
	}
	removed, _ := result.RowsAffected()
	return removed, nil
}

func scopeKey(userID string) string { return "u:" + userID }

// Windows are fixed and aligned rather than rolling, so "when does this
// reset" has an answer the interface can show. Minutes and five-hour blocks
// align to the epoch; weeks to Monday and months to the first, both in UTC,
// so an instance behaves the same wherever it runs.
func bucketStart(window Window, now time.Time) int64 {
	switch window {
	case WindowRPM, WindowTPM:
		return now.Truncate(time.Minute).UnixMilli()
	case Window5H:
		return now.Truncate(5 * time.Hour).UnixMilli()
	case WindowWeek:
		utc := now.UTC()
		// Go's Weekday starts at Sunday; the week here starts on Monday.
		offset := (int(utc.Weekday()) + 6) % 7
		day := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
		return day.AddDate(0, 0, -offset).UnixMilli()
	case WindowMonth:
		utc := now.UTC()
		return time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	default:
		return now.UnixMilli()
	}
}

func bucketEnd(window Window, now time.Time) time.Time {
	start := time.UnixMilli(bucketStart(window, now)).UTC()
	switch window {
	case WindowRPM, WindowTPM:
		return start.Add(time.Minute)
	case Window5H:
		return start.Add(5 * time.Hour)
	case WindowWeek:
		return start.AddDate(0, 0, 7)
	case WindowMonth:
		return start.AddDate(0, 1, 0)
	default:
		return start
	}
}

func round(value float64) float64 {
	return float64(int64(value*1000+0.5)) / 1000
}

// AsExceeded extracts the typed rejection from a wrapped error.
func AsExceeded(err error) (*ExceededError, bool) {
	var exceeded *ExceededError
	if errors.As(err, &exceeded) {
		return exceeded, true
	}
	return nil, false
}
