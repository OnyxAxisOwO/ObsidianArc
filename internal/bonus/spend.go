package bonus

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// Phase is which half of the spending order a request is in. Priority is what
// is spent before the quota windows; Fallback is what is spent when they have
// nothing left.
type Phase int

// Exclusive is a request for a model a pool in 'only' mode holds: the pools
// that cover it pay all of it or the request does not run, and nothing else is
// asked.
const (
	Priority Phase = iota
	Fallback
	Exclusive
)

// Hold is credits taken from one grant. What was taken is given back by
// Refund, so a request that never ran costs nothing.
type Hold struct {
	GrantID string  `json:"grant_id,omitempty"`
	Amount  float64 `json:"amount"`
	// A hold on a shared pool names the pool instead of a grant, with the
	// account and day its draw counts against and the period of the balance it
	// came out of. Exclusive is whether the pool was the only thing that could
	// pay.
	Pool      string `json:"pool,omitempty"`
	User      string `json:"user,omitempty"`
	Day       string `json:"day,omitempty"`
	Period    int64  `json:"period,omitempty"`
	Exclusive bool   `json:"exclusive,omitempty"`
}

// Total is the credits a set of holds covers.
func Total(holds []Hold) float64 {
	var sum float64
	for _, h := range holds {
		sum += h.Amount
	}
	return sum
}

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
// forced off, the account's bars it has switched off, and reserves. Within the
// same switch state, a grant that expires sooner goes first. Switched-on grants
// go before switched-off ones whatever their expiry, and reserves go last of all.
func (s *Store) candidates(ctx context.Context, q database.Queryer, userID, modelID string, phase Phase, now int64) ([]candidate, error) {
	rows, err := q.Query(ctx, `SELECT g.id, g.amount - g.used, g.expires_at, g.created_at,
		b.kind, b.toggle_mode, b.default_on, b.model_ids, c.enabled
		FROM bonus_grants g
		JOIN bonus_bars b ON b.id = g.bar_id
		LEFT JOIN bonus_choices c ON c.user_id = g.user_id AND c.bar_id = g.bar_id
		WHERE g.user_id = ? AND b.active = ? AND g.revoked_at = 0 AND g.amount - g.used > 0
		AND (g.expires_at = 0 OR g.expires_at > ?)`,
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
		ids, err := parseModels(models)
		if err != nil {
			// A grant is spent only where its bar can be read. Skipping it is
			// the safe answer; spending it would ignore the restriction.
			slog.Error("bonus: skipping a grant whose bar's model list is damaged", "grant", c.grantID, "error", err)
			continue
		}
		if !(Bar{ModelIDs: ids}).covers(modelID) {
			continue
		}
		c.reserve = kind == KindReserve
		// A reserve has no switch, so it is never spent in the priority phase
		// whatever the account's choice on the bar says.
		c.priority = !c.reserve && switchedOn(mode, defOn, enabled)
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
		// Switched on before switched off, whatever expires first. Without this
		// the fallback drains a switched-off bar that expires sooner while a
		// switched-on one still holds credit, which ModeOn and ModeOff rule out.
		if a.priority != b.priority {
			return a.priority
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
// run; it returns ErrInsufficient, and the claims made on the way there stay
// applied to q, so the caller's transaction must roll back on that error.
func (s *Store) Take(ctx context.Context, q database.Queryer, userID, modelID string, want float64, phase Phase) ([]Hold, error) {
	if want <= 0 {
		return nil, nil
	}
	at := time.Now()
	// The pools first: they are drawn on before an account's own grants, and
	// the lock order puts their rows ahead of any grant's.
	holds, need, short, err := s.takePools(ctx, q, userID, modelID, want, phase, at)
	if err != nil {
		return nil, err
	}
	if phase == Exclusive {
		if need > epsilon {
			if short == nil {
				short = &PoolExhaustedError{}
			}
			return nil, short
		}
		return holds, nil
	}
	now := at.UnixMilli()
	list, err := s.candidates(ctx, q, userID, modelID, phase, now)
	if err != nil {
		return nil, err
	}
	for _, c := range list {
		if need <= epsilon {
			break
		}
		remaining := c.remaining
		// No attempt cap. A claim loses only when the grant changed after it was
		// read: another take, a settlement or a revoke. Each retry re-reads the
		// grant, so the loop ends once no credit is left. A cap gave up with
		// credit still on the grant, and a fallback then refused a request the
		// grants together could cover.
		for remaining > epsilon && need > epsilon {
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
// hand out credit that was never granted. A revoked grant takes nothing back:
// its amount was cut to what it had been used for, so room given back here
// would be the revoked credit spendable again.
func (s *Store) Refund(ctx context.Context, q database.Queryer, holds []Hold) error {
	for _, h := range holds {
		if h.Amount <= 0 {
			continue
		}
		if h.Pool != "" {
			if err := refundPool(ctx, q, h); err != nil {
				return err
			}
			continue
		}
		if _, err := q.Exec(ctx, `UPDATE bonus_grants SET used =
			CASE WHEN used - ? < 0 THEN 0 ELSE used - ? END WHERE id = ? AND revoked_at = 0`, h.Amount, h.Amount, h.GrantID); err != nil {
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
//
// It must run inside the caller's transaction. The row lock taken on each grant
// is what keeps a settlement or a reservation of the same grant from reading the
// room this one is about to spend, and it lasts only until that transaction
// ends.
func (s *Store) Settle(ctx context.Context, q database.Queryer, holds []Hold, cost float64, released bool) (float64, error) {
	left := cost
	// The last hold on a pool that was the only payer takes whatever the others
	// could not: the windows were never asked, so nothing else will.
	last := -1
	for i, h := range holds {
		if h.Exclusive {
			last = i
		}
	}
	for i, h := range holds {
		if left <= epsilon {
			break
		}
		if h.Pool != "" {
			charged, err := s.settlePool(ctx, q, h, left, released, i == last)
			if err != nil {
				return 0, err
			}
			left -= charged
			continue
		}
		// Lock the grant before reading it: a read taken first can see the room
		// another settlement is about to spend, and on PostgreSQL's READ COMMITTED
		// both would charge it. Setting the column to itself takes the row lock
		// without changing the row, and the read after it sees what the lock holder
		// committed.
		if _, err := q.Exec(ctx, `UPDATE bonus_grants SET used = used WHERE id = ?`, h.GrantID); err != nil {
			return 0, fmt.Errorf("bonus: lock grant: %w", err)
		}
		var (
			amount, used float64
			revokedAt    int64
		)
		if err := q.QueryRow(ctx, `SELECT amount, used, revoked_at FROM bonus_grants WHERE id = ?`, h.GrantID).
			Scan(&amount, &used, &revokedAt); err != nil {
			if database.IsNotFound(err) {
				continue
			}
			return 0, err
		}
		// A revoked grant gives no room, whatever released says. Its amount was
		// cut to what it had been used for, and the hold still in flight is part
		// of that use: adding it back would let this request be covered by credit
		// the revoke has just taken away, and the figure would depend on whether
		// the release or the settlement happened first.
		if revokedAt != 0 {
			continue
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
