package quota

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// releaseGuard records that a reservation has been given back. Release holds
// its lock across the refund, so two releases of one reservation cannot both
// pass the check and both subtract.
//
// The mutex is process-local on purpose; a database row lock is not needed here.
// A Reservation lives only in the closure of the request that made it and is
// never persisted or handed to another instance, so no row exists for a second
// process to lock.
type releaseGuard struct {
	mu       sync.Mutex
	released bool
}

// Settle corrects a finished turn to what it actually cost. It runs after the
// answer, on a detached context, so a cancelled turn is still accounted for.
//
// The delta is signed: whatever ReserveFor took and the turn did not spend comes
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
	anchor, err := allowanceAnchor(ctx, s.db, account.CreatedAt)
	if err != nil {
		return err
	}

	return s.db.Tx(ctx, func(tx *database.Tx) error {
		return settleCounters(ctx, tx, account.ID, now, anchor, tokens, credits, minuteOnly)
	})
}

// settleCounters writes one settlement's share into the counters. It takes a
// Queryer so SettleTurn can put it in the same transaction as the bars it was
// partly paid from: a bar that is charged while the windows are not is a
// settlement that cost the account nothing.
func settleCounters(
	ctx context.Context, q database.Queryer, userID string, now time.Time, anchor int64,
	tokens int64, credits float64, minuteOnly int64,
) error {
	key := scopeKey(userID)
	// The bars' share of the minute goes in first, so this takes the minute row
	// before any window, as the lock order in service.go requires.
	if minuteOnly != 0 {
		if _, err := bump(ctx, q, key, WindowTPM, bucketStart(WindowTPM, now, 0), 0, minuteOnly, 0); err != nil {
			return err
		}
	}
	for _, window := range tokenWindows {
		if _, err := bump(ctx, q, key, window, bucketStart(window, now, anchor), 0, tokens, credits); err != nil {
			return err
		}
	}
	return nil
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

	// The bars and the windows are charged in one transaction. Split in two, a
	// failure of the second left the bars charged and the windows never told,
	// and the turn was free. The anchor is read before the transaction, as
	// settleWindowsMinute reads it, so the reset lock is not taken here.
	now := time.Now()
	anchor, err := allowanceAnchor(ctx, s.db, account.CreatedAt)
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		covered, err := s.bonus.Settle(ctx, tx, f.holds, actual.Credits, released)
		if err != nil {
			return err
		}
		// The rest of the cost, and the same share of the tokens. A turn that cost
		// nothing in credits was not paid for by a bar and counts in full. The
		// tokens a bar paid for still count toward the per-minute limit, which is
		// what the reservation charged it.
		share := 1.0
		if actual.Credits > bonus.Epsilon {
			share = math.Max(0, 1-covered/actual.Credits)
		}
		carried := int64(math.Round(float64(actual.Tokens) * share))
		credits := math.Max(0, actual.Credits-covered)
		minuteOnly := actual.Tokens - carried
		if carried == 0 && credits == 0 && minuteOnly == 0 {
			return nil
		}
		return settleCounters(ctx, tx, account.ID, now, anchor, carried, credits, minuteOnly)
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
	f := reserved.funding
	windows := reserved.taken && !reserved.estimate.empty()
	if !windows && reserved.covered == 0 && f == nil {
		return nil
	}
	// Held until the refund commits, so a second release waits for the first
	// and then finds it done. A failed release leaves the guard open, which is
	// what lets the caller try again.
	if g := reserved.guard; g != nil {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.released {
			return nil
		}
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
		// The minute, then the windows: the order the lock order in service.go gives them.
		for _, window := range tokenWindows {
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
	if err == nil && reserved.guard != nil {
		reserved.guard.released = true
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
