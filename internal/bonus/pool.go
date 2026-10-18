package bonus

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// A shared pool is a bar of KindShared. Its balance is the instance's, not an
// account's: everybody draws on the one figure for the models the bar names,
// so there are no grants in it. docs/architecture/bonus-and-checkin.md has the
// rules; this is the balance, its reset and the per-account daily limit.

// Pool is a shared bar's balance and the rules that refill and ration it.
type Pool struct {
	Amount float64 `json:"amount"`
	Used   float64 `json:"used"`
	// "" never resets; PeriodDay, PeriodWeek or PeriodMonth puts the balance
	// back to ResetAmount when one starts.
	ResetPeriod string  `json:"reset_period"`
	ResetAmount float64 `json:"reset_amount"`
	PeriodStart int64   `json:"period_start"`
	// The most one account may draw in a day; zero is no limit.
	PerUserDaily float64 `json:"per_user_daily"`
}

const (
	PeriodDay   = "day"
	PeriodWeek  = "week"
	PeriodMonth = "month"
)

// Remaining is what is left of the balance.
func (p Pool) Remaining() float64 { return math.Max(p.Amount-p.Used, 0) }

var (
	// ErrSharedNoGrants is a grant into a shared pool, which has a balance of
	// its own rather than amounts held by accounts.
	ErrSharedNoGrants = errors.New("bonus: a shared pool is topped up, not granted into")
	// ErrNotShared is a balance change asked of a bar that is not a pool.
	ErrNotShared = errors.New("bonus: that bonus is not a shared pool")
	// ErrKindChange is a save that would turn a pool into a bar or a bar into a
	// pool. Grants and a pool's balance are different things, so neither could
	// carry over.
	ErrKindChange  = errors.New("bonus: a shared pool cannot become another kind, nor another kind a pool")
	ErrInvalidPool = errors.New("bonus: a pool's reset is never, day, week or month, and its amounts are between zero and a million")
)

// PoolExhaustedError is a request for a model only a pool may pay for, which
// the pool could not cover. It is ErrInsufficient as well, so the paths that
// test for that see it as what it is.
type PoolExhaustedError struct {
	Name string
	// Whether it was the account's daily limit rather than the balance.
	Capped bool
	// When the limit that refused it lifts; zero when nothing is scheduled to
	// refill the balance.
	ResetsAt int64
}

func (e *PoolExhaustedError) Error() string {
	if e.Capped {
		return fmt.Sprintf("bonus: today's share of the pool %q is spent", e.Name)
	}
	return fmt.Sprintf("bonus: the pool %q is empty", e.Name)
}

func (e *PoolExhaustedError) Unwrap() error { return ErrInsufficient }

func (p *Pool) normalise() error {
	switch p.ResetPeriod {
	case "", PeriodDay, PeriodWeek, PeriodMonth:
	default:
		return ErrInvalidPool
	}
	for _, v := range []float64{p.ResetAmount, p.PerUserDaily, p.Amount} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > MaxAmount {
			return ErrInvalidPool
		}
	}
	if p.ResetPeriod != "" && p.ResetAmount <= 0 {
		return ErrInvalidPool
	}
	return nil
}

func (s *Store) loc() *time.Location {
	if s.Location != nil {
		if loc := s.Location(); loc != nil {
			return loc
		}
	}
	return time.UTC
}

// periodStart is the start of the period t falls in, in loc. Weeks start on
// Monday.
func periodStart(period string, t time.Time, loc *time.Location) int64 {
	t = t.In(loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	switch period {
	case PeriodDay:
		return day.UnixMilli()
	case PeriodWeek:
		return day.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7)).UnixMilli()
	case PeriodMonth:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc).UnixMilli()
	}
	return 0
}

// nextPeriod is when the period that started at start ends.
func nextPeriod(period string, start int64, loc *time.Location) int64 {
	t := time.UnixMilli(start).In(loc)
	switch period {
	case PeriodDay:
		return t.AddDate(0, 0, 1).UnixMilli()
	case PeriodWeek:
		return t.AddDate(0, 0, 7).UnixMilli()
	case PeriodMonth:
		return t.AddDate(0, 1, 0).UnixMilli()
	}
	return 0
}

func dayOf(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02") }

func nextDay(t time.Time, loc *time.Location) int64 {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc).UnixMilli()
}

// current is the pool as it stands at now: one whose period has ended reads as
// already reset, which is what the next draw on it will make it.
func (p Pool) current(now time.Time, loc *time.Location) Pool {
	if p.ResetPeriod == "" {
		return p
	}
	if start := periodStart(p.ResetPeriod, now, loc); p.PeriodStart < start {
		p.Amount, p.Used, p.PeriodStart = p.ResetAmount, 0, start
	}
	return p
}

const poolColumns = `amount, used, reset_period, reset_amount, period_start, per_user_daily`

func scanPool(row scanner) (Pool, error) {
	var p Pool
	err := row.Scan(&p.Amount, &p.Used, &p.ResetPeriod, &p.ResetAmount, &p.PeriodStart, &p.PerUserDaily)
	return p, err
}

func (s *Store) pool(ctx context.Context, q database.Queryer, barID string) (Pool, error) {
	p, err := scanPool(q.QueryRow(ctx, `SELECT `+poolColumns+` FROM bonus_pools WHERE bar_id = ?`, barID))
	if err != nil {
		if database.IsNotFound(err) {
			return Pool{}, ErrNotFound
		}
		return Pool{}, fmt.Errorf("bonus: read pool: %w", err)
	}
	return p, nil
}

// pools reads every pool, by bar, as it stands now.
func (s *Store) pools(ctx context.Context) (map[string]Pool, error) {
	rows, err := s.db.Query(ctx, `SELECT bar_id, `+poolColumns+` FROM bonus_pools`)
	if err != nil {
		return nil, fmt.Errorf("bonus: list pools: %w", err)
	}
	defer rows.Close()
	now, loc := time.Now(), s.loc()
	out := map[string]Pool{}
	for rows.Next() {
		var (
			barID string
			p     Pool
		)
		if err := rows.Scan(&barID, &p.Amount, &p.Used, &p.ResetPeriod, &p.ResetAmount, &p.PeriodStart, &p.PerUserDaily); err != nil {
			return nil, fmt.Errorf("bonus: scan pool: %w", err)
		}
		out[barID] = p.current(now, loc)
	}
	return out, rows.Err()
}

func (s *Store) insertPool(ctx context.Context, q database.Queryer, barID string, p Pool, now time.Time) error {
	if p.ResetPeriod != "" {
		p.PeriodStart = periodStart(p.ResetPeriod, now, s.loc())
		if p.Amount == 0 {
			p.Amount = p.ResetAmount
		}
	}
	_, err := q.Exec(ctx, `INSERT INTO bonus_pools (bar_id, `+poolColumns+`, updated_at)
		VALUES (?, ?, 0, ?, ?, ?, ?, ?)`,
		barID, p.Amount, p.ResetPeriod, p.ResetAmount, p.PeriodStart, p.PerUserDaily, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("bonus: create pool: %w", err)
	}
	return nil
}

// updatePoolRules changes how a pool refills and rations, not what is in it.
// A period that changes starts counting from the one now under way, so
// switching a reset on does not empty the balance into a reset at once.
func (s *Store) updatePoolRules(ctx context.Context, q database.Queryer, barID string, p Pool, now time.Time) error {
	if _, err := q.Exec(ctx, `UPDATE bonus_pools SET used = used WHERE bar_id = ?`, barID); err != nil {
		return fmt.Errorf("bonus: lock pool: %w", err)
	}
	stored, err := s.pool(ctx, q, barID)
	if errors.Is(err, ErrNotFound) {
		// A shared bar always has its row; one that lost it gets a fresh one
		// rather than a save that fails for good.
		return s.insertPool(ctx, q, barID, p, now)
	}
	if err != nil {
		return err
	}
	start := stored.PeriodStart
	if p.ResetPeriod != stored.ResetPeriod {
		start = periodStart(p.ResetPeriod, now, s.loc())
	}
	_, err = q.Exec(ctx, `UPDATE bonus_pools SET reset_period = ?, reset_amount = ?, period_start = ?,
		per_user_daily = ?, updated_at = ? WHERE bar_id = ?`,
		p.ResetPeriod, p.ResetAmount, start, p.PerUserDaily, now.UnixMilli(), barID)
	if err != nil {
		return fmt.Errorf("bonus: update pool: %w", err)
	}
	return nil
}

// lockPool takes the pool's row, applies a reset that is due, and returns the
// pool as it now is. Every draw, settlement and change goes through the lock:
// the balance is one row everybody shares, and a read taken without it could
// see room another request is about to spend.
func (s *Store) lockPool(ctx context.Context, q database.Queryer, barID string, now time.Time) (Pool, error) {
	if _, err := q.Exec(ctx, `UPDATE bonus_pools SET used = used WHERE bar_id = ?`, barID); err != nil {
		return Pool{}, fmt.Errorf("bonus: lock pool: %w", err)
	}
	p, err := s.pool(ctx, q, barID)
	if err != nil {
		return Pool{}, err
	}
	if next := p.current(now, s.loc()); next.PeriodStart != p.PeriodStart {
		if _, err := q.Exec(ctx, `UPDATE bonus_pools SET amount = ?, used = 0, period_start = ? WHERE bar_id = ?`,
			next.Amount, next.PeriodStart, barID); err != nil {
			return Pool{}, fmt.Errorf("bonus: reset pool: %w", err)
		}
		p = next
	}
	return p, nil
}

// AdjustPool changes what is in a pool: add puts amount in (or takes it out,
// when negative, down to nothing left); set makes what is left exactly amount.
// A reset that is due is applied first, so the change is made to the balance
// the next request would see.
func (s *Store) AdjustPool(ctx context.Context, barID string, set bool, amount float64) (Pool, error) {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || math.Abs(amount) > MaxAmount || (set && amount < 0) {
		return Pool{}, ErrInvalidAmount
	}
	var out Pool
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		bar, err := s.Bar(ctx, tx, barID)
		if err != nil {
			return err
		}
		if bar.Kind != KindShared {
			return ErrNotShared
		}
		p, err := s.lockPool(ctx, tx, barID, time.Now())
		if err != nil {
			return err
		}
		left := amount
		if !set {
			left = p.Remaining() + amount
		}
		left = min(math.Max(left, 0), MaxAmount)
		p.Amount = p.Used + left
		if _, err := tx.Exec(ctx, `UPDATE bonus_pools SET amount = ?, updated_at = ? WHERE bar_id = ?`,
			p.Amount, time.Now().UnixMilli(), barID); err != nil {
			return fmt.Errorf("bonus: adjust pool: %w", err)
		}
		out = p
		return nil
	})
	return out, err
}

// Exclusive reports whether a request for modelID may be paid only by a pool:
// some switched-on pool in 'only' mode names it.
func (s *Store) Exclusive(ctx context.Context, modelID string) (bool, error) {
	rows, err := s.db.Query(ctx, `SELECT b.model_ids FROM bonus_bars b JOIN bonus_pools p ON p.bar_id = b.id
		WHERE b.kind = ? AND b.toggle_mode = ? AND b.active = ?`, KindShared, ModeOnly, true)
	if err != nil {
		return false, fmt.Errorf("bonus: find exclusive pools: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return false, err
		}
		ids, err := parseModels(raw)
		if err != nil {
			// The restriction cannot be read, so it is not known which models it
			// holds to the pool. Treating none as held is the side that never
			// refuses a request the pool was never meant to stand in front of.
			continue
		}
		if (Bar{ModelIDs: ids}).covers(modelID) {
			return true, nil
		}
	}
	return false, rows.Err()
}

type poolCandidate struct {
	barID string
	name  string
}

// poolCandidates are the pools a request for modelID may draw on in this
// phase, in the order they were made.
func (s *Store) poolCandidates(ctx context.Context, q database.Queryer, userID, modelID string, phase Phase) ([]poolCandidate, error) {
	rows, err := q.Query(ctx, `SELECT b.id, b.name, b.toggle_mode, b.default_on, b.model_ids, c.enabled
		FROM bonus_bars b JOIN bonus_pools p ON p.bar_id = b.id
		LEFT JOIN bonus_choices c ON c.user_id = ? AND c.bar_id = b.id
		WHERE b.kind = ? AND b.active = ?
		ORDER BY b.created_at, b.id`, userID, KindShared, true)
	if err != nil {
		return nil, fmt.Errorf("bonus: find pools: %w", err)
	}
	defer rows.Close()
	var out []poolCandidate
	for rows.Next() {
		var (
			c       poolCandidate
			mode    Mode
			defOn   bool
			models  string
			enabled *bool
		)
		if err := rows.Scan(&c.barID, &c.name, &mode, &defOn, &models, &enabled); err != nil {
			return nil, fmt.Errorf("bonus: scan pool: %w", err)
		}
		ids, err := parseModels(models)
		if err != nil || !(Bar{ModelIDs: ids}).covers(modelID) {
			continue
		}
		on := mode == ModeOn
		if mode == ModeUser {
			on = defOn
			if enabled != nil {
				on = *enabled
			}
		}
		switch phase {
		case Exclusive:
			// Every pool that covers the model pays here, whatever its switch: the
			// account has no other way to pay for it.
		case Priority:
			if mode == ModeOnly || !on {
				continue
			}
		case Fallback:
			if mode == ModeOnly {
				continue
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// takePools draws up to want from the pools, inside the caller's transaction,
// and returns what it took and what is still needed. capped says whether a
// daily limit, rather than an empty balance, is what left some of it unpaid.
//
// Lock order: a pool's row, then the account's usage row in it, one pool at a
// time in the order they were made; grants come after every pool. Settle and
// Refund walk the holds in the order they were taken, which is this one.
func (s *Store) takePools(ctx context.Context, q database.Queryer, userID, modelID string, want float64, phase Phase, now time.Time) ([]Hold, float64, *PoolExhaustedError, error) {
	list, err := s.poolCandidates(ctx, q, userID, modelID, phase)
	if err != nil {
		return nil, want, nil, err
	}
	loc := s.loc()
	day := dayOf(now, loc)
	var (
		holds []Hold
		short *PoolExhaustedError
	)
	need := want
	for _, c := range list {
		if need <= epsilon {
			break
		}
		p, err := s.lockPool(ctx, q, c.barID, now)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, want, nil, err
		}
		room := p.Remaining()
		if room <= epsilon {
			if short == nil {
				short = &PoolExhaustedError{Name: c.name, ResetsAt: nextPeriod(p.ResetPeriod, p.PeriodStart, loc)}
			}
			continue
		}
		used, err := lockUsage(ctx, q, c.barID, userID, day)
		if err != nil {
			return nil, want, nil, err
		}
		// Whichever of the balance and the share ran short is what the account is
		// told, with when that one lifts.
		capped := false
		if p.PerUserDaily > 0 {
			if left := p.PerUserDaily - used; left < room {
				room, capped = left, true
			}
		}
		if need > room+epsilon && short == nil {
			short = &PoolExhaustedError{Name: c.name, Capped: capped, ResetsAt: nextPeriod(p.ResetPeriod, p.PeriodStart, loc)}
			if capped {
				short.ResetsAt = nextDay(now, loc)
			}
		}
		if room <= epsilon {
			continue
		}
		take := min(room, need)
		if _, err := q.Exec(ctx, `UPDATE bonus_pools SET used = used + ? WHERE bar_id = ?`, take, c.barID); err != nil {
			return nil, want, nil, fmt.Errorf("bonus: draw on pool: %w", err)
		}
		if _, err := q.Exec(ctx, `UPDATE bonus_pool_usage SET used = used + ? WHERE bar_id = ? AND user_id = ? AND day = ?`,
			take, c.barID, userID, day); err != nil {
			return nil, want, nil, fmt.Errorf("bonus: count draw: %w", err)
		}
		holds = append(holds, Hold{
			Amount: take, Pool: c.barID, User: userID, Day: day, Period: p.PeriodStart, Exclusive: phase == Exclusive,
		})
		need -= take
	}
	if need > epsilon && short == nil && len(list) > 0 {
		short = &PoolExhaustedError{Name: list[0].name}
	}
	return holds, need, short, nil
}

// lockUsage takes the account's row for the day in a pool, making it if it is
// not there, and says how much has been drawn on it.
func lockUsage(ctx context.Context, q database.Queryer, barID, userID, day string) (float64, error) {
	if _, err := q.Exec(ctx, `INSERT INTO bonus_pool_usage (bar_id, user_id, day, used) VALUES (?, ?, ?, 0)
		ON CONFLICT (bar_id, user_id, day) DO UPDATE SET used = bonus_pool_usage.used`, barID, userID, day); err != nil {
		return 0, fmt.Errorf("bonus: lock pool usage: %w", err)
	}
	var used float64
	if err := q.QueryRow(ctx, `SELECT used FROM bonus_pool_usage WHERE bar_id = ? AND user_id = ? AND day = ?`,
		barID, userID, day).Scan(&used); err != nil {
		return 0, fmt.Errorf("bonus: read pool usage: %w", err)
	}
	return used, nil
}

// settlePool charges up to left of a turn's cost to the pool a hold came
// from, and returns what it charged. force charges all of left whatever room
// there is: a turn only the pool could pay for has nowhere else to go, and the
// overshoot is the same one the windows take when a turn outruns its estimate.
func (s *Store) settlePool(ctx context.Context, q database.Queryer, h Hold, left float64, released, force bool) (float64, error) {
	p, err := s.lockPool(ctx, q, h.Pool, time.Now())
	if errors.Is(err, ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	// A reset since the hold was taken wiped the hold with the rest of the
	// period, so there is no hold left in used to make room for.
	held := !released && p.PeriodStart == h.Period
	room := p.Amount - p.Used
	if held {
		room += h.Amount
	}
	if p.PerUserDaily > 0 {
		used, err := lockUsage(ctx, q, h.Pool, h.User, h.Day)
		if err != nil {
			return 0, err
		}
		capRoom := p.PerUserDaily - used
		if !released {
			capRoom += h.Amount
		}
		room = min(room, capRoom)
	}
	charge := min(left, room)
	if force {
		charge = left
	}
	if charge <= 0 {
		return 0, nil
	}
	if _, err := q.Exec(ctx, `UPDATE bonus_pools SET used = used + ? WHERE bar_id = ?`, charge, h.Pool); err != nil {
		return 0, fmt.Errorf("bonus: settle pool: %w", err)
	}
	if _, err := q.Exec(ctx, `INSERT INTO bonus_pool_usage (bar_id, user_id, day, used) VALUES (?, ?, ?, ?)
		ON CONFLICT (bar_id, user_id, day) DO UPDATE SET used = bonus_pool_usage.used + excluded.used`,
		h.Pool, h.User, h.Day, charge); err != nil {
		return 0, fmt.Errorf("bonus: settle pool usage: %w", err)
	}
	return charge, nil
}

// refundPool gives a hold back to its pool, unless the pool has reset since:
// the reset took the hold away with everything else, and giving it back would
// add to a balance that was just set.
func refundPool(ctx context.Context, q database.Queryer, h Hold) error {
	if _, err := q.Exec(ctx, `UPDATE bonus_pools SET used = CASE WHEN used - ? < 0 THEN 0 ELSE used - ? END
		WHERE bar_id = ? AND period_start = ?`, h.Amount, h.Amount, h.Pool, h.Period); err != nil {
		return fmt.Errorf("bonus: refund pool: %w", err)
	}
	if _, err := q.Exec(ctx, `UPDATE bonus_pool_usage SET used = CASE WHEN used - ? < 0 THEN 0 ELSE used - ? END
		WHERE bar_id = ? AND user_id = ? AND day = ?`, h.Amount, h.Amount, h.Pool, h.User, h.Day); err != nil {
		return fmt.Errorf("bonus: refund pool usage: %w", err)
	}
	return nil
}

// PrunePoolUsage forgets the daily draws older than keep. Only today's row is
// ever held against a limit; the rest is there for the administrator's figures.
func (s *Store) PrunePoolUsage(ctx context.Context, keep time.Duration) error {
	cutoff := dayOf(time.Now().Add(-keep), s.loc())
	if _, err := s.db.Exec(ctx, `DELETE FROM bonus_pool_usage WHERE day < ?`, cutoff); err != nil {
		return fmt.Errorf("bonus: prune pool usage: %w", err)
	}
	return nil
}

// drawnToday is what each account drew from a pool today, for the account's
// own view of its daily limit.
func (s *Store) drawnToday(ctx context.Context, userID string, now time.Time) (map[string]float64, error) {
	rows, err := s.db.Query(ctx, `SELECT bar_id, used FROM bonus_pool_usage WHERE user_id = ? AND day = ?`,
		userID, dayOf(now, s.loc()))
	if err != nil {
		return nil, fmt.Errorf("bonus: read pool usage: %w", err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var (
			barID string
			used  float64
		)
		if err := rows.Scan(&barID, &used); err != nil {
			return nil, err
		}
		out[barID] = used
	}
	return out, rows.Err()
}

// drawersToday is how many accounts drew on each pool today.
func (s *Store) drawersToday(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.Query(ctx, `SELECT bar_id, COUNT(*) FROM bonus_pool_usage WHERE day = ? AND used > 0 GROUP BY bar_id`,
		dayOf(time.Now(), s.loc()))
	if err != nil {
		return nil, fmt.Errorf("bonus: count pool usage: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var (
			barID string
			n     int
		)
		if err := rows.Scan(&barID, &n); err != nil {
			return nil, err
		}
		out[barID] = n
	}
	return out, rows.Err()
}
