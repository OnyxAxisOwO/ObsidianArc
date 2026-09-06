package quota

import (
	"context"
	"errors"
	"fmt"
	"sync"
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

	// Generations in flight per account. See Begin.
	mu       sync.Mutex
	inFlight map[string]int
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

// Estimate is the most a turn could cost. Reserve takes it up front and
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

// Reserve claims one request and the turn's worst case against every window
// that applies, and fails if any of them is already spent.
//
// The whole thing runs in one transaction. Each window's counter is
// incremented and read back in a single statement, so the check sees a value
// no concurrent request can have moved underneath it; if any window is over,
// the transaction rolls back and every increment goes with it.
//
// The estimate is what makes that true across requests rather than only
// within one. Charging nothing until a turn finished meant ten long
// generations started together all saw the same untouched counter and all
// passed; the allowance was only enforced against turns that had already
// ended. Reserving the ceiling means the tenth is refused while the first
// nine are still streaming, and Settle hands back the difference.
func (s *Service) Reserve(ctx context.Context, account user.User, estimate Estimate) (Reservation, error) {
	policy, err := s.PolicyFor(ctx, nil, account)
	if err != nil {
		return Reservation{}, err
	}
	if policy.Unlimited() {
		// Nothing was charged, so there is nothing to give back. Saying so is
		// what stops the release from subtracting a reservation that was never
		// taken: the counters are what the usage screen reads, and an account
		// exempt from the limits was having its own reading wiped to zero on
		// every turn.
		return Reservation{}, nil
	}
	if estimate.Tokens < 0 {
		estimate.Tokens = 0
	}
	if estimate.Credits < 0 {
		estimate.Credits = 0
	}

	now := time.Now()
	key := scopeKey(account.ID)

	err = s.db.Tx(ctx, func(tx *database.Tx) error {
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
			counter, err := bump(ctx, tx, key, WindowTPM, bucketStart(WindowTPM, now),
				0, estimate.Tokens, 0)
			if err != nil {
				return err
			}
			if counter.Tokens > *policy.TPM {
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
			counter, err := bump(ctx, tx, key, window, start, 1, estimate.Tokens, estimate.Credits)
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
			// The counter now includes this turn's worst case, so the
			// comparison is "would finishing this put you over" rather than
			// "were you already over" — which is the question that has an
			// answer while ten turns are in flight at once.
			if limits.Tokens != nil && *limits.Tokens > 0 && counter.Tokens > *limits.Tokens {
				return &ExceededError{
					Window: window, Dimension: "tokens",
					Used: float64(counter.Tokens), Limit: float64(*limits.Tokens), ResetsAt: resets,
				}
			}
			if limits.Credits != nil && *limits.Credits > 0 && counter.Credits > *limits.Credits {
				return &ExceededError{
					Window: window, Dimension: "credits",
					Used: counter.Credits, Limit: *limits.Credits, ResetsAt: resets,
				}
			}
		}
		return nil
	})
	if err != nil {
		// The transaction rolled back, so nothing is outstanding.
		return Reservation{}, err
	}
	return Reservation{at: now, estimate: estimate, taken: true}, nil
}

// Reservation is what Reserve charged, and the moment it charged it.
//
// Release needs both. The counters are bucketed by window, and a generation
// that runs for a minute — or for five hours, at the edge of that window — can
// finish in a later bucket than it started in. Giving the reservation back at
// the time of release put a negative delta into a bucket that had never been
// charged, where the floor at zero swallowed it: the old bucket kept a charge
// that was never spent, and the new one lost the turn that was.
//
// The zero value means nothing was reserved, which is what an account exempt
// from the limits gets, and releasing it does nothing at all.
type Reservation struct {
	at       time.Time
	estimate Estimate
	taken    bool
}

// Settle corrects a finished turn to what it actually cost. It runs after the
// answer, on a detached context, so a cancelled turn is still accounted for.
//
// The delta is signed: whatever Reserve took and the turn did not spend comes
// back, which is what stops a generous ceiling from being a real charge. A
// turn that overshot its estimate — the model ignored max_tokens, say —
// settles upward instead.
//
// It touches every window unconditionally rather than only the enforced ones:
// the counters are also what the usage display reads, and a limit turned on
// tomorrow should not start from zero for someone who has been using the
// server all week.
func (s *Service) Settle(ctx context.Context, userID string, reserved, actual Estimate) error {
	tokens := actual.Tokens - reserved.Tokens
	credits := actual.Credits - reserved.Credits
	if tokens == 0 && credits == 0 {
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

// Release gives a reservation back — for a turn that never ran, and for the
// part of one that was reserved and not spent.
//
// Into the bucket it came out of, not the current one. What the turn really
// cost is settled separately, at the time it finished, which is where that
// cost belongs; the two together leave the old bucket even and the new one
// carrying the turn.
func (s *Service) Release(ctx context.Context, userID string, reserved Reservation) error {
	if !reserved.taken || reserved.estimate.empty() {
		return nil
	}
	key := scopeKey(userID)
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		for _, window := range []Window{WindowTPM, Window5H, WindowWeek, WindowMonth} {
			if _, err := bump(ctx, tx, key, window, bucketStart(window, reserved.at),
				0, -reserved.estimate.Tokens, -reserved.estimate.Credits); err != nil {
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
		   -- Refunds arrive here as negative deltas. Clamped, because two
		   -- settles racing on the same row must not leave a counter below
		   -- zero and hand out free allowance. CASE rather than GREATEST or
		   -- MAX: one of those is Postgres-only and the other is an aggregate
		   -- there.
		   tokens   = CASE WHEN usage_counters.tokens + excluded.tokens < 0
		                   THEN 0 ELSE usage_counters.tokens + excluded.tokens END,
		   credits  = CASE WHEN usage_counters.credits + excluded.credits < 0
		                   THEN 0 ELSE usage_counters.credits + excluded.credits END
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

// --- concurrency --------------------------------------------------------------

// How many generations one account may have running at once.
//
// Reserving the ceiling already bounds what concurrent turns can spend, but
// only for an account that has a limit at all; an unlimited one, or a window
// that is measured and not enforced, would still let a script hold open as
// many upstream connections as it likes. This is the backstop on connections
// rather than on cost, and it is small because a person cannot read four
// answers at once.
const MaxConcurrentPerUser = 4

var ErrTooManyInFlight = errors.New("quota: too many generations in flight for this account")

// Begin claims a slot and returns the release. The release is idempotent, so
// a caller may defer it and still call it early.
//
// In memory, like the other counters that exist only to shape one process's
// behaviour: two instances behind a load balancer each enforce their own, and
// the cost ceiling above is what holds in that case.
func (s *Service) Begin(userID string) (func(), error) {
	s.mu.Lock()
	if s.inFlight == nil {
		s.inFlight = map[string]int{}
	}
	if s.inFlight[userID] >= MaxConcurrentPerUser {
		s.mu.Unlock()
		return nil, ErrTooManyInFlight
	}
	s.inFlight[userID]++
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.inFlight[userID] <= 1 {
				delete(s.inFlight, userID)
			} else {
				s.inFlight[userID]--
			}
			s.mu.Unlock()
		})
	}, nil
}
