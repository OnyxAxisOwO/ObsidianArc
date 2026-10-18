package quota

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// ReserveFor claims one request and the turn's worst case against every
// window that applies, for a request to a particular model, which is what
// decides which bonus bars may pay for it, and fails if any of them is
// already spent. modelID may be empty and reset may be nil.
//
// Each step runs in one transaction. Each window's counter is incremented
// and read back in a single statement, so the check sees a value no
// concurrent request can have moved underneath it; if any window is over,
// the transaction rolls back and every increment goes with it.
//
// The estimate is what makes that true across requests rather than only
// within one. Charging nothing until a turn finished meant ten long
// generations started together all saw the same untouched counter and all
// passed; the allowance was only enforced against turns that had already
// ended. Reserving the ceiling means the tenth is refused while the first
// nine are still streaming, and Settle hands back the difference.
//
// When reset is set, a rejected reservation is retried after atomically
// spending a reset. Locking the account row serialises this choice across
// server processes; otherwise a burst arriving at an empty allowance could
// spend several cards for the same reset.
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
	// A model a pool holds alone is paid by the pool or not at all: not the
	// windows, not the account's own bars, not a reset card.
	if s.bonus != nil && estimate.Credits > 0 && modelID != "" {
		only, err := s.bonus.Exclusive(ctx, modelID)
		if err != nil {
			return Reservation{}, err
		}
		if only {
			return s.reserveFromBonus(ctx, account, modelID, estimate, policy, now, bonus.Exclusive)
		}
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
		if fallback, ferr := s.reserveFromBonus(ctx, account, modelID, estimate, policy, now, bonus.Fallback); ferr == nil {
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

// reserveFromBonus pays for a request out of the bonus rows, whole or not at
// all. phase picks the rows: Fallback is what was kept back for when the
// windows have nothing left, Exclusive is what only a pool may pay for. The
// allowance windows are not touched: they have nothing left, and counting the
// request against them would only push them further over. The burst limits
// are: a row is more allowance, not a way round them.
func (s *Service) reserveFromBonus(
	ctx context.Context, account user.User, modelID string, estimate Estimate, policy Policy, now time.Time, phase bonus.Phase,
) (Reservation, error) {
	var holds []bonus.Hold
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// The rows come before the minute, as in the lock order in service.go. A refund
		// of one of these holds takes the row and then the minute, so taking the
		// minute first here could leave each side holding the row the other wants.
		claimed, takeErr := s.bonus.Take(ctx, tx, account.ID, modelID, estimate.Credits, phase)
		if takeErr != nil && !errors.Is(takeErr, bonus.ErrInsufficient) {
			return takeErr
		}
		// A row that cannot pay whole is decided after the minute, so a request
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
	res := Reservation{at: now, funding: &funding{holds: holds}, guard: &releaseGuard{}}
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
	res := Reservation{at: now, estimate: charged, taken: !whole, anchor: anchor, guard: &releaseGuard{}}
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
	if covered >= estimate.Credits-bonus.Epsilon {
		return Estimate{}, true
	}
	share := 1 - covered/estimate.Credits
	return Estimate{Tokens: int64(math.Round(float64(estimate.Tokens) * share)), Credits: estimate.Credits - covered}, false
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
// order in service.go gives the allowance rows. Only the limit check depends on whether
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

// Reservation is what ReserveFor charged, and the moment it charged it.
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
	// A pointer for the same reason: every copy of the reservation shares one
	// guard, so giving it back twice refunds it once.
	guard *releaseGuard
}
