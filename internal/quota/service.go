package quota

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
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
//  3. The bonus_grants rows that a request is paid from, refunded to, or settled
//     against.
//  4. The account's usage_counters rows, one window kind at a time, in the order
//     of counterKinds. A transaction takes at most one row of each kind.
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
	return s.reserve(ctx, account, "", estimate, nil, time.Now())
}

// ReserveWithAutoReset retries one rejected reservation after atomically
// spending a reset. Locking the account row serialises this choice across
// server processes; otherwise a burst arriving at an empty allowance could
// spend several cards for the same reset.
func (s *Service) ReserveWithAutoReset(
	ctx context.Context, account user.User, estimate Estimate, reset AutoReset,
) (Reservation, error) {
	return s.reserve(ctx, account, "", estimate, reset, time.Now())
}

// ReserveFor is Reserve for a request to a particular model, which is what
// decides which bonus bars may pay for it. reset may be nil.
//
// The order is written down in docs/architecture/bonus-and-checkin.md: the
// bars that are switched on, then the windows, then the bars that were kept
// back, then a reset card. Each step is its own transaction that either
// reserves the whole request or leaves nothing behind, so a step that fails
// has cost nothing and the next one starts clean.
func (s *Service) ReserveFor(
	ctx context.Context, account user.User, modelID string, estimate Estimate, reset AutoReset,
) (Reservation, error) {
	return s.reserve(ctx, account, modelID, estimate, reset, time.Now())
}

// reserve takes now from its caller rather than reading the clock at each step,
// so that every step of one reservation lands in the same buckets.
func (s *Service) reserve(
	ctx context.Context, account user.User, modelID string, estimate Estimate, reset AutoReset, now time.Time,
) (Reservation, error) {
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

	res, err := s.reserveOnce(ctx, account, modelID, estimate, nil, policy, now)
	if err == nil {
		return res, nil
	}
	exceeded, ok := AsExceeded(err)
	if !ok || exceeded.Window == WindowRPM || exceeded.Window == WindowTPM {
		// A burst-rate refusal clears in a minute; neither a bar kept back nor a
		// card is spent on it.
		return Reservation{}, err
	}
	if s.bonus != nil && estimate.Credits > 0 {
		if fallback, ferr := s.reserveFallback(ctx, account, modelID, estimate, policy, now); ferr == nil {
			return fallback, nil
		} else if !errors.Is(ferr, bonus.ErrInsufficient) {
			return Reservation{}, ferr
		}
	}
	if reset != nil {
		return s.reserveOnce(ctx, account, modelID, estimate, reset, policy, now)
	}
	return Reservation{}, err
}

// reserveFallback pays for a request out of the bars that were kept back,
// whole or not at all. The allowance windows are not touched: they have nothing
// left, and counting the request against them would only push them further
// over. The burst limits are: a bar is more allowance, not a way round them.
func (s *Service) reserveFallback(
	ctx context.Context, account user.User, modelID string, estimate Estimate, policy Policy, now time.Time,
) (Reservation, error) {
	var holds []bonus.Hold
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// The bars come before the minute, as in the lock order above. A refund
		// of one of these holds takes the bar and then the minute, so taking the
		// minute first here could leave each side holding the row the other wants.
		claimed, takeErr := s.bonus.Take(ctx, tx, account.ID, modelID, estimate.Credits, bonus.Fallback)
		if takeErr != nil && !errors.Is(takeErr, bonus.ErrInsufficient) {
			return takeErr
		}
		// A bar that cannot pay whole is decided after the minute, so a request
		// the minute refuses is still reported as that refusal, the same as when
		// the minute was checked first.
		if err := reserveRate(ctx, tx, policy, scopeKey(account.ID), estimate.Tokens, now); err != nil {
			return err
		}
		if takeErr != nil {
			return takeErr
		}
		holds = claimed
		return nil
	})
	if err != nil {
		return Reservation{}, err
	}
	res := Reservation{at: now, funding: &funding{holds: holds}}
	if policy.tokenRateOn() {
		res.covered = estimate.Tokens
	}
	return s.noted(ctx, res), nil
}

func (s *Service) reserveOnce(
	ctx context.Context, account user.User, modelID string, estimate Estimate, reset AutoReset, policy Policy, now time.Time,
) (Reservation, error) {
	key := scopeKey(account.ID)

	var (
		anchor  int64
		holds   []bonus.Hold
		charged = estimate
		whole   bool
	)
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		holds, charged, whole = nil, estimate, false
		var err error
		anchor, err = lockedAllowanceAnchor(ctx, tx, account.CreatedAt)
		if err != nil {
			return err
		}
		if reset != nil {
			if _, err := tx.Exec(ctx,
				`UPDATE users SET updated_at = updated_at WHERE id = ?`, account.ID); err != nil {
				return fmt.Errorf("quota: lock account for automatic reset: %w", err)
			}
		}

		// The bars that are switched on pay first, and what they pay does not
		// count against the allowance windows. What they cannot cover is the
		// windows'. The burst limits count the whole request whoever pays.
		if s.bonus != nil && estimate.Credits > 0 {
			holds, err = s.bonus.Take(ctx, tx, account.ID, modelID, estimate.Credits, bonus.Priority)
			if err != nil {
				return err
			}
			charged, whole = uncovered(estimate, bonus.Total(holds))
		}

		// The minute is charged once, here. The walk below may run again after a
		// reset card has cleared the window, and that retry does not charge the
		// minute a second time.
		if err := reserveRate(ctx, tx, policy, key, estimate.Tokens, now); err != nil {
			return err
		}
		if whole {
			return nil
		}
		walkErr := reserveAllowance(ctx, tx, policy, key, anchor, charged, now)
		if walkErr == nil || reset == nil {
			return walkErr
		}
		exceeded, ok := AsExceeded(walkErr)
		if !ok {
			return walkErr
		}

		// A reset card restores allowance; spending one on a burst-rate refusal
		// would trade a permanent item for a limit that clears in a minute. The
		// minute was checked above, so only an allowance window reaches here.
		resetWindows, spent, err := reset(ctx, tx, exceeded.Window)
		if err != nil {
			return err
		}
		if !spent {
			return walkErr
		}
		if err := clearAllowance(ctx, tx, key, anchor, now, resetWindows); err != nil {
			return fmt.Errorf("quota: automatic reset: %w", err)
		}
		return reserveAllowance(ctx, tx, policy, key, anchor, charged, now)
	})
	if err != nil {
		// The transaction rolled back, so nothing is outstanding and a failed
		// replacement reservation did not consume the card.
		return Reservation{}, err
	}
	res := Reservation{at: now, estimate: charged, taken: !whole, anchor: anchor}
	if policy.tokenRateOn() {
		res.covered = estimate.Tokens - charged.Tokens
	}
	if len(holds) > 0 {
		res.funding = &funding{holds: holds, windows: !whole}
	}
	return s.noted(ctx, res), nil
}

// uncovered is what of an estimate the windows still have to carry once the
// bars have paid covered credits of it, and whether the bars paid all of it.
// Tokens are carried in proportion, because a request half paid for out of a
// bar should not count all its tokens against the window.
func uncovered(estimate Estimate, covered float64) (Estimate, bool) {
	if covered <= 0 {
		return estimate, false
	}
	if covered >= estimate.Credits-1e-9 {
		return Estimate{}, true
	}
	share := 1 - covered/estimate.Credits
	return Estimate{Tokens: int64(math.Round(float64(estimate.Tokens) * share)), Credits: estimate.Credits - covered}, false
}

// clearAllowance is what a reset card does inside the reservation that spent it:
// it deletes the current bucket of each allowance window the card names.
//
// Only the current bucket goes. A limit check reads only that bucket, and so does
// the usage screen, so older buckets are not part of what a reset restores.
// Deleting them as well would take more than one row of a window kind in this
// transaction, which the lock order forbids, and an older bucket is exactly what
// a release of an earlier reservation can hold. The minute is not an allowance
// window: a card never clears it, and the charge this request put on it stays as
// reserveRate left it.
func clearAllowance(ctx context.Context, tx database.Queryer, key string, anchor int64, now time.Time, windows []string) error {
	for _, window := range AllowanceWindows {
		if !resetsWindow(windows, window) {
			continue
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM usage_counters WHERE scope_key = ? AND window_kind = ? AND window_start = ?`,
			key, window, bucketStart(window, now, anchor)); err != nil {
			return err
		}
	}
	return nil
}

// deleteCounters deletes the counter rows that a reset names, under the global
// reset row. A single DELETE takes its rows in whatever order the database scans
// them, which can put a later kind's row ahead of an earlier one's and so
// reverse the lock order. One statement per kind, in counterKinds order, does
// not. No two resets can meet, nor a reset and a prune, because all of them hold
// the global reset row first.
// scope, when not empty, narrows the rows, with args for its placeholders.
func deleteCounters(ctx context.Context, tx database.Queryer, names []string, scope string, args ...any) error {
	for _, window := range counterKinds {
		if !resetsWindow(names, window) {
			continue
		}
		query := `DELETE FROM usage_counters WHERE window_kind = ?`
		kindArgs := []any{window}
		if scope != "" {
			query += ` AND ` + scope
			kindArgs = append(kindArgs, args...)
		}
		if _, err := tx.Exec(ctx, query, kindArgs...); err != nil {
			return err
		}
	}
	return nil
}

// resetsWindow is whether a reset that names these windows clears window. An
// empty list, or one that names "full", clears every window.
func resetsWindow(names []string, window Window) bool {
	named := false
	for _, name := range names {
		name = strings.TrimSpace(name)
		switch {
		case name == "full":
			return true
		case name == "":
		case Window(name) == window:
			return true
		default:
			named = true
		}
	}
	return !named
}

// tokenRateOn is whether the per-minute token limit is in force, and so
// whether a reservation put anything in its bucket to give back.
func (p Policy) tokenRateOn() bool { return p.TPM != nil && *p.TPM > 0 }

// reserveRate charges the two burst limits: one request, and tokens. They are
// held to every request the server admits, whoever ends up paying for it —
// they protect the providers behind the server, which a bonus bar does not
// change.
func reserveRate(ctx context.Context, tx database.Queryer, policy Policy, key string, tokens int64, now time.Time) error {
	// The per-minute buckets are wall-clock, so they need no account anchor.
	if policy.RPM != nil && *policy.RPM > 0 {
		counter, err := bump(ctx, tx, key, WindowRPM, bucketStart(WindowRPM, now, 0), 1, 0, 0)
		if err != nil {
			return err
		}
		if counter.Requests > *policy.RPM {
			return &ExceededError{
				Window: WindowRPM, Dimension: "requests",
				Used: float64(counter.Requests), Limit: float64(*policy.RPM),
				ResetsAt: bucketEnd(WindowRPM, now, 0),
			}
		}
	}

	if policy.TPM != nil && *policy.TPM > 0 {
		counter, err := bump(ctx, tx, key, WindowTPM, bucketStart(WindowTPM, now, 0), 0, tokens, 0)
		if err != nil {
			return err
		}
		if counter.Tokens > *policy.TPM {
			return &ExceededError{
				Window: WindowTPM, Dimension: "tokens",
				Used: float64(counter.Tokens), Limit: float64(*policy.TPM),
				ResetsAt: bucketEnd(WindowTPM, now, 0),
			}
		}
	}
	return nil
}

// reserveAllowance charges the three allowance windows what the request costs
// them: the whole estimate, or the part of it the bonus bars did not pay.
//
// Every window is charged, enforced or not, in one pass in AllowanceWindows
// order: the usage screen reads all of them, and a limit switched on later has
// to start from what the account has really used. That order is the one the lock
// order above gives the allowance rows. Only the limit check depends on whether
// a window is on.
//
// A refused walk takes back what it charged, the refused window included. A
// reset card retries the walk inside this transaction, and without that the
// retry would count the request twice in every window it had already passed.
func reserveAllowance(
	ctx context.Context,
	tx database.Queryer,
	policy Policy,
	key string,
	anchor int64,
	estimate Estimate,
	now time.Time,
) error {
	// Settle and Release move the same deltas through every window. A window
	// left out of the charge would have the release take the turn back out of
	// it, and the zero floor would keep none of it.
	for i, window := range AllowanceWindows {
		counter, err := bump(ctx, tx, key, window, bucketStart(window, now, anchor), 1, estimate.Tokens, estimate.Credits)
		if err != nil {
			return err
		}
		limits := policy.Windows[window]
		if !limits.isOn() {
			continue
		}
		refused := windowRefusal(window, limits, counter, bucketEnd(window, now, anchor))
		if refused == nil {
			continue
		}
		for _, charged := range AllowanceWindows[:i+1] {
			if _, err := bump(ctx, tx, key, charged, bucketStart(charged, now, anchor),
				-1, -estimate.Tokens, -estimate.Credits); err != nil {
				return err
			}
		}
		return refused
	}
	return nil
}

// windowRefusal is the rejection a window's counter earns, or nil. The counter
// already includes this turn's worst case, so the comparison is "would finishing
// this put you over" rather than "were you already over", which is the question
// that has an answer while ten turns are in flight at once.
func windowRefusal(window Window, limits Limits, used counter, resets time.Time) *ExceededError {
	if limits.Requests != nil && *limits.Requests > 0 && used.Requests > *limits.Requests {
		return &ExceededError{
			Window: window, Dimension: "requests",
			Used: float64(used.Requests), Limit: float64(*limits.Requests), ResetsAt: resets,
		}
	}
	if limits.Tokens != nil && *limits.Tokens > 0 && used.Tokens > *limits.Tokens {
		return &ExceededError{
			Window: window, Dimension: "tokens",
			Used: float64(used.Tokens), Limit: float64(*limits.Tokens), ResetsAt: resets,
		}
	}
	if limits.Credits != nil && *limits.Credits > 0 && used.Credits > *limits.Credits {
		return &ExceededError{
			Window: window, Dimension: "credits",
			Used: used.Credits, Limit: *limits.Credits, ResetsAt: resets,
		}
	}
	return nil
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
	// The account's created_at, carried so the release lands in the same
	// bucket the reservation came out of. The windows are per account now,
	// so the moment alone no longer identifies one.
	anchor int64
	// The tokens of the request that bonus bars paid for, which the allowance
	// windows do not carry and the per-minute window does.
	covered int64
	// What bonus bars paid of it, if any. A pointer, so that releasing the
	// reservation and settling the turn — which happen in either order, on
	// copies of this value — see each other.
	funding *funding
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
// server all week. The reset anchor is read before the short write transaction:
// if a global reset overlaps this accounting, the charge stays in the older
// bucket and the reset's new epoch ignores it. Unrelated completions do not
// queue on the reset row.
func (s *Service) Settle(ctx context.Context, account user.User, reserved, actual Estimate) error {
	return s.settleWindows(ctx, account, actual.Tokens-reserved.Tokens, actual.Credits-reserved.Credits)
}

func (s *Service) settleWindows(ctx context.Context, account user.User, tokens int64, credits float64) error {
	return s.settleWindowsMinute(ctx, account, tokens, credits, 0)
}

// settleWindowsMinute is settleWindows for a turn bonus bars paid part of:
// minuteOnly are the tokens they paid for, which count toward the per-minute
// limit and toward no allowance window.
func (s *Service) settleWindowsMinute(ctx context.Context, account user.User, tokens int64, credits float64, minuteOnly int64) error {
	if tokens == 0 && credits == 0 && minuteOnly == 0 {
		return nil
	}
	now := time.Now()
	key := scopeKey(account.ID)
	anchor, err := allowanceAnchor(ctx, s.db, account.CreatedAt)
	if err != nil {
		return err
	}

	return s.db.Tx(ctx, func(tx *database.Tx) error {
		// The bars' share of the minute goes in first, so this transaction takes
		// the minute row before any window, as the lock order above requires.
		if minuteOnly != 0 {
			if _, err := bump(ctx, tx, key, WindowTPM, bucketStart(WindowTPM, now, 0), 0, minuteOnly, 0); err != nil {
				return err
			}
		}
		for _, window := range []Window{WindowTPM, Window5H, WindowWeek, WindowMonth} {
			if _, err := bump(ctx, tx, key, window, bucketStart(window, now, anchor), 0, tokens, credits); err != nil {
				return err
			}
		}
		return nil
	})
}

// SettleTurn is Settle for a turn whose reservation may have been paid for,
// in whole or part, by bonus bars: what the bars cover is charged to them, and
// only the rest reaches the windows. ctx is the request's, which is where the
// reservation was noted; a turn with nothing noted — one that was never
// reserved, or paid for wholly by the windows — settles as Settle does.
func (s *Service) SettleTurn(ctx context.Context, account user.User, actual Estimate) error {
	f := takeFunding(ctx)
	if f == nil || s.bonus == nil {
		return s.Settle(ctx, account, Estimate{}, actual)
	}
	f.mu.Lock()
	released := f.released
	f.mu.Unlock()

	var covered float64
	if err := s.db.Tx(ctx, func(tx *database.Tx) error {
		var err error
		covered, err = s.bonus.Settle(ctx, tx, f.holds, actual.Credits, released)
		return err
	}); err != nil {
		return err
	}
	// The rest of the cost, and the same share of the tokens. A turn that cost
	// nothing in credits was not paid for by a bar and counts in full. The
	// tokens a bar paid for still count toward the per-minute limit, which is
	// what the reservation charged it.
	share := 1.0
	if actual.Credits > 1e-9 {
		share = math.Max(0, 1-covered/actual.Credits)
	}
	carried := int64(math.Round(float64(actual.Tokens) * share))
	return s.settleWindowsMinute(ctx, account, carried, math.Max(0, actual.Credits-covered), actual.Tokens-carried)
}

// Release gives a reservation back — for a turn that never ran, and for the
// part of one that was reserved and not spent.
//
// Into the bucket it came out of, not the current one. What the turn really
// cost is settled separately, at the time it finished, which is where that
// cost belongs; the two together leave the old bucket even and the new one
// carrying the turn.
func (s *Service) Release(ctx context.Context, userID string, reserved Reservation) error {
	f := reserved.funding
	windows := reserved.taken && !reserved.estimate.empty()
	if !windows && reserved.covered == 0 && f == nil {
		return nil
	}
	key := scopeKey(userID)
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if f != nil {
			if err := s.bonus.Refund(ctx, tx, f.holds); err != nil {
				return err
			}
		}
		if reserved.covered > 0 {
			if _, err := bump(ctx, tx, key, WindowTPM, bucketStart(WindowTPM, reserved.at, 0),
				0, -reserved.covered, 0); err != nil {
				return err
			}
		}
		if !windows {
			return nil
		}
		// The minute, then the windows: the order the lock order above gives them.
		for _, window := range []Window{WindowTPM, Window5H, WindowWeek, WindowMonth} {
			if _, err := bump(ctx, tx, key, window, bucketStart(window, reserved.at, reserved.anchor),
				0, -reserved.estimate.Tokens, -reserved.estimate.Credits); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && f != nil {
		f.mu.Lock()
		f.released = true
		f.mu.Unlock()
	}
	return err
}

// RecordRejection counts a refused request against the rate window only, so a
// client retrying in a loop still runs into the rate limit rather than being
// free to hammer the endpoint.
func (s *Service) RecordRejection(ctx context.Context, userID string) {
	now := time.Now()
	// The rate window only, which is wall-clock, so this one needs no anchor.
	_, _ = bump(ctx, s.db, scopeKey(userID), WindowRPM, bucketStart(WindowRPM, now, 0), 1, 0, 0)
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

// SummariesFor is SummaryFor for a whole list of accounts, in three queries
// instead of a few per account: the policies, the global reset, and every
// live counter. It is what an administrator's overview of everyone's
// allowance reads, where one query per account per window would make the
// page's cost grow with the instance.
//
// The answers are the ones SummaryFor gives each account — the resolution and
// the bucket arithmetic are the same code — and a counter for a window that
// has since rolled over is simply not found, which reads as nothing spent,
// exactly as it does there.
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

	// Bounded by the same two months PruneCounters keeps, which is longer than
	// any allowance window lives.
	now := time.Now()
	type slot struct {
		scope  string
		window Window
		start  int64
	}
	counters := map[slot]counter{}
	rows, err := s.db.Query(ctx,
		`SELECT scope_key, window_kind, window_start, requests, tokens, credits FROM usage_counters
		 WHERE window_kind IN (?, ?, ?) AND window_start >= ?`,
		Window5H, WindowWeek, WindowMonth, now.AddDate(0, -2, 0).UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("quota: read counters: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key slot
		var used counter
		if err := rows.Scan(&key.scope, &key.window, &key.start, &used.Requests, &used.Tokens, &used.Credits); err != nil {
			return nil, fmt.Errorf("quota: scan counter: %w", err)
		}
		counters[key] = used
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	display := s.settings.UsageDisplay()
	out := make(map[string]Summary, len(accounts))
	for _, account := range accounts {
		policy := newPolicy(ScopeUser, account.ID)
		if !s.exempt(account) {
			layers := []Policy{global}
			if account.GroupID != "" {
				layers = append(layers, groups[account.GroupID])
			}
			policy = Resolve(append(layers, users[account.ID])...)
		}
		anchor := anchorFor(account.CreatedAt, resetAt)
		summary := Summary{Unlimited: policy.Unlimited(), Display: display, Windows: make([]WindowUsage, 0, len(AllowanceWindows))}
		for _, window := range AllowanceWindows {
			used := counters[slot{scopeKey(account.ID), window, bucketStart(window, now, anchor)}]
			summary.Windows = append(summary.Windows, windowUsage(window, policy.Windows[window], used, now, anchor))
		}
		out[account.ID] = summary
	}
	return out, nil
}

// PruneCounters drops buckets that have rolled over. Monthly buckets are the
// longest-lived, so anything older than two months is certainly dead.
//
// Each chunk takes the global reset row before its first delete, as the lock order
// requires. Without that the prune could meet an administrator's reset, which
// deletes these rows one kind at a time, and PostgreSQL would abort one of them.
// The chunks keep the row held briefly, because every reservation waits on it.
func (s *Service) PruneCounters(ctx context.Context) (int64, error) {
	cutoff := time.Now().AddDate(0, -2, 0).UnixMilli()
	var removed int64
	for {
		n, err := s.pruneChunk(ctx, cutoff)
		removed += n
		if err != nil {
			return removed, fmt.Errorf("quota: prune counters: %w", err)
		}
		if n < pruneChunkRows {
			return removed, nil
		}
	}
}

// pruneChunkRows bounds one prune transaction. The rows are taken oldest first,
// so a chunk that comes back short has reached the end of the stale rows.
const pruneChunkRows = 1000

func (s *Service) pruneChunk(ctx context.Context, cutoff int64) (int64, error) {
	var removed int64
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockAllowanceBoundary(ctx, tx); err != nil {
			return err
		}
		// The subquery names the rows, because DELETE ... LIMIT is not portable.
		result, err := tx.Exec(ctx,
			`DELETE FROM usage_counters WHERE (scope_key, window_kind, window_start) IN (
			   SELECT scope_key, window_kind, window_start FROM usage_counters
			   WHERE window_start < ? ORDER BY window_start LIMIT ?)`,
			cutoff, pruneChunkRows)
		if err != nil {
			return err
		}
		removed, err = result.RowsAffected()
		return err
	})
	return removed, err
}

// The one place the per-account scope key is spelled, so ResetGroup can build
// the same key in SQL without the two drifting apart.
const userScopePrefix = "u:"

func scopeKey(userID string) string { return userScopePrefix + userID }

// The global reset is an allowance boundary rather than a second kind of
// counter. Keeping one timestamp means a reset reaches every existing account
// without rewriting their registration dates; an account created afterwards
// still starts at its own later creation time.
const globalResetKey = "quota.global_reset_at"

func allowanceAnchor(ctx context.Context, q database.Queryer, createdAt int64) (int64, error) {
	resetAt, err := globalResetAt(ctx, q)
	if err != nil {
		return 0, err
	}
	return anchorFor(createdAt, resetAt), nil
}

// globalResetAt is zero when no reset has ever been made.
func globalResetAt(ctx context.Context, q database.Queryer) (int64, error) {
	var raw string
	err := q.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, globalResetKey).Scan(&raw)
	if database.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("quota: read global reset: %w", err)
	}
	resetAt, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, nil
	}
	return resetAt, nil
}

func anchorFor(createdAt, resetAt int64) int64 {
	if resetAt <= createdAt {
		return createdAt
	}
	return resetAt
}

// lockAllowanceBoundary takes the global reset row, the first lock in the lock
// order above. A reservation that charges the allowance windows, every
// administrator reset and every prune take it. This no-op upsert takes the same
// database row lock ResetAll writes, including when two server processes share the
// database.
func lockAllowanceBoundary(ctx context.Context, tx database.Queryer) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET updated_at = settings.updated_at`,
		globalResetKey, "0", time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("quota: lock global reset: %w", err)
	}
	return nil
}

func lockedAllowanceAnchor(ctx context.Context, tx database.Queryer, createdAt int64) (int64, error) {
	// A reset and a reservation must agree which side of the boundary the
	// allowance check belongs to, so the reservation reads the anchor under the
	// same lock the reset writes it under.
	if err := lockAllowanceBoundary(ctx, tx); err != nil {
		return 0, err
	}
	return allowanceAnchor(ctx, tx, createdAt)
}

// ResetAll puts every account's allowance back to its full amount and starts
// each allowance window again from now.
//
// The counters are deleted rather than zeroed. An absent row and a row at
// zero read the same to everything above this — bump() upserts, SummaryFor
// treats a missing row as nothing spent — and deleting leaves nothing behind
// to go stale when the window it belonged to rolls over.
//
// The ledger is deliberately untouched. What was spent is still what was
// spent; this is a decision about what may be spent next, and rewriting the
// record to agree with it would destroy the only account of either.
func (s *Service) ResetAll(ctx context.Context) error {
	now := time.Now().UnixMilli()
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// Reservations hold this row lock so a limit check cannot be written
		// against an obsolete allowance after the counters have been cleared.
		// Settlement reads the epoch before its write transaction, allowing
		// independent completed turns to update their own counters concurrently.
		if _, err := tx.Exec(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			globalResetKey, strconv.FormatInt(now, 10), now); err != nil {
			return err
		}
		return deleteCounters(ctx, tx, nil, "")
	})
	if err != nil {
		return fmt.Errorf("quota: reset all: %w", err)
	}
	return nil
}

// ResetGroup does the same for everyone in a group.
//
// Named by the group rather than by its members, because enumerating them
// first needs a page size — and a page size on this path is a silent cap on
// how much of a group a reset actually reaches. It was one: the caller asked
// the user list for exactly as many rows as the group has, and the list
// clamps anything above two hundred back down to fifty, so resetting a large
// group quietly reset fifty accounts and reported that it had.
//
// The scope key is built in SQL with the same "u:" prefix scopeKey writes,
// concatenated with || because that is the one string operator both engines
// spell the same way.
func (s *Service) ResetGroup(ctx context.Context, groupID string) error {
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockAllowanceBoundary(ctx, tx); err != nil {
			return err
		}
		return deleteCounters(ctx, tx, nil,
			`scope_key IN (SELECT ? || id FROM users WHERE group_id = ?)`, userScopePrefix, groupID)
	})
	if err != nil {
		return fmt.Errorf("quota: reset group: %w", err)
	}
	return nil
}

// ResetWindows clears usage counters for specific windows on named accounts.
// An empty window list or one containing "full" clears every window.
func (s *Service) ResetWindows(ctx context.Context, userIDs []string, windows []string) error {
	const batch = 200
	for start := 0; start < len(userIDs); start += batch {
		end := min(start+batch, len(userIDs))
		chunk := userIDs[start:end]

		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, userID := range chunk {
			placeholders[i] = "?"
			args[i] = scopeKey(userID)
		}
		scope := `scope_key IN (` + strings.Join(placeholders, ", ") + `)`

		err := s.db.Tx(ctx, func(tx *database.Tx) error {
			if err := lockAllowanceBoundary(ctx, tx); err != nil {
				return err
			}
			return deleteCounters(ctx, tx, windows, scope, args...)
		})
		if err != nil {
			return fmt.Errorf("quota: reset windows: %w", err)
		}
	}
	return nil
}

// Reset does the same for named accounts across all windows.
//
// In batches, because the caller may hand it every member of a group and one
// statement with ten thousand placeholders is a statement no database wants.
func (s *Service) Reset(ctx context.Context, userIDs []string) error {
	return s.ResetWindows(ctx, userIDs, nil)
}

// Windows are fixed and aligned rather than rolling, so "when does this
// reset" has an answer the interface can show. Minutes and five-hour blocks
// align to the epoch; weeks to Monday and months to the first, both in UTC,
// so an instance behaves the same wherever it runs.
// bucketStart is the moment the window a turn falls in began.
//
// The rate windows stay on the wall clock: a minute is a minute, and giving
// each account its own would only make two accounts disagree about when one
// ends.
//
// The allowance windows are anchored to the account, and run from the moment
// it registered. They used to roll over at the same instant for everybody,
// which is two problems in one: somebody who signed up on the 28th got three
// days of a "month", and every allowance on the instance came back at
// midnight on the 1st, which is when everyone arrives at once.
//
// `anchor` is the account's created_at in epoch milliseconds. Zero — an
// account with no creation time, which no live row has — falls back to the
// epoch, so the arithmetic below is still well defined.
func bucketStart(window Window, now time.Time, anchor int64) int64 {
	switch window {
	case WindowRPM, WindowTPM:
		return now.Truncate(time.Minute).UnixMilli()
	case Window5H:
		return periodStart(now, anchor, 5*time.Hour)
	case WindowWeek:
		return periodStart(now, anchor, 7*24*time.Hour)
	case WindowMonth:
		from := time.UnixMilli(anchor).UTC()
		return addMonths(from, monthsSince(now, from)).UnixMilli()
	default:
		return now.UnixMilli()
	}
}

// periodStart walks whole periods from the anchor to the one `now` is in.
func periodStart(now time.Time, anchor int64, period time.Duration) int64 {
	elapsed := now.UnixMilli() - anchor
	if elapsed <= 0 {
		// A clock behind the account's own creation. There is nothing to
		// have spent yet, so it counts as the first period.
		return anchor
	}
	size := period.Milliseconds()
	return anchor + (elapsed/size)*size
}

// monthsSince is how many whole calendar months have passed since the anchor,
// so a monthly allowance renews on the day of the month somebody signed up on
// rather than every thirty days, drifting backwards through the year.
func monthsSince(now time.Time, from time.Time) int {
	utc := now.UTC()
	if !utc.After(from) {
		return 0
	}
	months := (utc.Year()-from.Year())*12 + int(utc.Month()) - int(from.Month())
	if addMonths(from, months).After(utc) {
		// The day of the month has not come round yet this month.
		months--
	}
	if months < 0 {
		return 0
	}
	return months
}

// addMonths keeps the day of the month, clamped to the length of the target
// one: an account created on the 31st renews on the 28th in February rather
// than sliding into March, which is what AddDate would do with it.
func addMonths(from time.Time, months int) time.Time {
	year, month, day := from.Date()
	hour, minute, second := from.Clock()
	target := time.Date(year, month+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(target.Year(), target.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		day = last
	}
	return time.Date(target.Year(), target.Month(), day, hour, minute, second, 0, time.UTC)
}

func bucketEnd(window Window, now time.Time, anchor int64) time.Time {
	start := time.UnixMilli(bucketStart(window, now, anchor)).UTC()
	switch window {
	case WindowRPM, WindowTPM:
		return start.Add(time.Minute)
	case Window5H:
		return start.Add(5 * time.Hour)
	case WindowWeek:
		return start.AddDate(0, 0, 7)
	case WindowMonth:
		// Not start.AddDate(0, 1, 0): the anchor's day of the month is what
		// the next one lands on, and adding a month to a clamped 28th would
		// walk the renewal backwards a day at a time.
		from := time.UnixMilli(anchor).UTC()
		return addMonths(from, monthsSince(now, from)+1)
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
const (
	DefaultMaxConcurrent = 4
	MaxConcurrentPerUser = DefaultMaxConcurrent
)

var ErrTooManyInFlight = errors.New("quota: too many generations in flight for this account")

// Begin claims a slot and returns the release. The release is idempotent, so
// a caller may defer it and still call it early. A maxConcurrent <= 0 means
// unlimited concurrency for the account.
//
// In memory, like the other counters that exist only to shape one process's
// behaviour: two instances behind a load balancer each enforce their own, and
// the cost ceiling above is what holds in that case.
func (s *Service) Begin(userID string, maxConcurrent int) (func(), error) {
	if maxConcurrent <= 0 {
		return func() {}, nil
	}
	s.mu.Lock()
	if s.inFlight == nil {
		s.inFlight = map[string]int{}
	}
	if s.inFlight[userID] >= maxConcurrent {
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
