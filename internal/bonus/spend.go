package bonus

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// Phase is which half of the spending order a request is in. Priority is what
// is spent before the quota windows; Fallback is what is spent when they have
// nothing left.
type Phase int

const (
	Priority Phase = iota
	Fallback
)

// Hold is credits taken from one grant. What was taken is given back by
// Refund, so a request that never ran costs nothing.
type Hold struct {
	GrantID string  `json:"grant_id"`
	Amount  float64 `json:"amount"`
}

func total(holds []Hold) float64 {
	var sum float64
	for _, h := range holds {
		sum += h.Amount
	}
	return sum
}

// Total is the credits a set of holds covers.
func Total(holds []Hold) float64 { return total(holds) }

type candidate struct {
	grantID   string
	remaining float64
	reserve   bool
	priority  bool
	expiresAt int64
	created   int64
}

// candidates are the grants an account could spend on a request for modelID
// right now, in the order they would be spent.
//
// Priority ones are those that are switched on — forced on, or the account's
// own bars it has left on. Everything else that could pay is a fallback:
// forced off, the account's bars it has switched off, and reserves. A reserve
// is last of all, and a grant that expires sooner goes before one that lasts.
func (s *Store) candidates(ctx context.Context, q database.Queryer, userID, modelID string, phase Phase, now int64) ([]candidate, error) {
	rows, err := q.Query(ctx, `SELECT g.id, g.amount - g.used, g.expires_at, g.created_at,
		b.kind, b.toggle_mode, b.default_on, b.model_ids, c.enabled
		FROM bonus_grants g
		JOIN bonus_bars b ON b.id = g.bar_id
		LEFT JOIN bonus_choices c ON c.user_id = g.user_id AND c.bar_id = g.bar_id
		WHERE g.user_id = ? AND b.active = ? AND g.amount - g.used > 0 AND (g.expires_at = 0 OR g.expires_at > ?)`,
		userID, true, now)
	if err != nil {
		return nil, fmt.Errorf("bonus: find grants: %w", err)
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var (
			c       candidate
			kind    Kind
			mode    Mode
			defOn   bool
			models  string
			enabled *bool
		)
		if err := rows.Scan(&c.grantID, &c.remaining, &c.expiresAt, &c.created, &kind, &mode, &defOn, &models, &enabled); err != nil {
			return nil, fmt.Errorf("bonus: scan grant: %w", err)
		}
		if !(Bar{ModelIDs: parseModels(models)}).covers(modelID) {
			continue
		}
		c.reserve = kind == KindReserve
		switch {
		case c.reserve, mode == ModeOff:
			c.priority = false
		case mode == ModeOn:
			c.priority = true
		default:
			c.priority = defOn
			if enabled != nil {
				c.priority = *enabled
			}
		}
		if phase == Priority && !c.priority {
			continue
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.reserve != b.reserve {
			return !a.reserve
		}
		// Expiring grants first; never-expiring ones after.
		if (a.expiresAt == 0) != (b.expiresAt == 0) {
			return a.expiresAt != 0
		}
		if a.expiresAt != b.expiresAt {
			return a.expiresAt < b.expiresAt
		}
		if a.created != b.created {
			return a.created < b.created
		}
		return a.grantID < b.grantID
	})
	return out, nil
}

// Take claims up to want credits for a request, inside the caller's
// transaction, and says which grants gave how much.
//
// Each grant is claimed with one conditional update — "add to used, if that
// still fits" — so two requests taking from the same grant at once cannot both
// have the last of it; a claim that loses is retried against what is now left.
//
// A Priority take gives what there is, up to want: the rest is the windows'
// business. A Fallback take is all or nothing, because it is spent when the
// windows have nothing to give and a request half-paid for is not one that can
// run; it returns ErrInsufficient and takes nothing, and the caller's
// transaction is what puts back what was claimed on the way there.
func (s *Store) Take(ctx context.Context, q database.Queryer, userID, modelID string, want float64, phase Phase) ([]Hold, error) {
	if want <= 0 {
		return nil, nil
	}
	now := time.Now().UnixMilli()
	list, err := s.candidates(ctx, q, userID, modelID, phase, now)
	if err != nil {
		return nil, err
	}
	var holds []Hold
	need := want
	for _, c := range list {
		if need <= epsilon {
			break
		}
		remaining := c.remaining
		for attempt := 0; attempt < 3 && remaining > epsilon && need > epsilon; attempt++ {
			take := min(remaining, need)
			res, err := q.Exec(ctx, `UPDATE bonus_grants SET used = used + ? WHERE id = ? AND amount - used >= ?`,
				take, c.grantID, take)
			if err != nil {
				return nil, fmt.Errorf("bonus: claim: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				holds = append(holds, Hold{GrantID: c.grantID, Amount: take})
				need -= take
				break
			}
			// Somebody else took some first: look again at what is left.
			var amount, used float64
			if err := q.QueryRow(ctx, `SELECT amount, used FROM bonus_grants WHERE id = ?`, c.grantID).Scan(&amount, &used); err != nil {
				return nil, err
			}
			remaining = amount - used
		}
	}
	if phase == Fallback && need > epsilon {
		return nil, ErrInsufficient
	}
	return holds, nil
}

// epsilon is what a floating-point sum of prices may be off by.
const epsilon = 1e-9

// Refund gives holds back. Never below zero used: two refunds racing must not
// hand out credit that was never granted.
func (s *Store) Refund(ctx context.Context, q database.Queryer, holds []Hold) error {
	for _, h := range holds {
		if h.Amount <= 0 {
			continue
		}
		if _, err := q.Exec(ctx, `UPDATE bonus_grants SET used =
			CASE WHEN used - ? < 0 THEN 0 ELSE used - ? END WHERE id = ?`, h.Amount, h.Amount, h.GrantID); err != nil {
			return fmt.Errorf("bonus: refund: %w", err)
		}
	}
	return nil
}

// Settle charges what a request really cost to the grants that were held for
// it, and returns how much of the cost they covered — the rest belongs to the
// quota windows.
//
// released says whether the reservation has already been given back. If it has
// not, each grant's used still includes its hold, so the room it has is what
// is left plus the hold; charging on top of a hold that is about to be refunded
// leaves the grant carrying exactly the cost and no more, whichever of the two
// happens first.
func (s *Store) Settle(ctx context.Context, q database.Queryer, holds []Hold, cost float64, released bool) (float64, error) {
	left := cost
	for _, h := range holds {
		if left <= epsilon {
			break
		}
		var amount, used float64
		if err := q.QueryRow(ctx, `SELECT amount, used FROM bonus_grants WHERE id = ?`, h.GrantID).Scan(&amount, &used); err != nil {
			if database.IsNotFound(err) {
				continue
			}
			return 0, err
		}
		room := amount - used
		if !released {
			room += h.Amount
		}
		charge := min(left, room)
		if charge <= 0 {
			continue
		}
		if _, err := q.Exec(ctx, `UPDATE bonus_grants SET used = used + ? WHERE id = ?`, charge, h.GrantID); err != nil {
			return 0, fmt.Errorf("bonus: settle: %w", err)
		}
		left -= charge
	}
	return cost - left, nil
}
