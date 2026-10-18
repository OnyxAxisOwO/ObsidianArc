package bonus

import (
	"context"
	"fmt"
	"math"
	"time"
)

// View is one bar as its holder sees it. Whether the amounts are in it is the
// bar's own rule: a bar that keeps its total to itself shows a proportion.
type View struct {
	BarID       string `json:"bar_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        Kind   `json:"kind"`
	ToggleMode  Mode   `json:"toggle_mode"`
	// Choosable is whether the holder may switch it; Enabled is whether it is
	// being spent first right now.
	Choosable bool `json:"choosable"`
	Enabled   bool `json:"enabled"`
	ShowTotal bool `json:"show_total"`
	// Nil when the bar does not show its amounts.
	Total     *float64 `json:"total"`
	Remaining *float64 `json:"remaining"`
	// Of what is unspent and unexpired, out of everything unexpired: always
	// given, whatever the bar shows.
	Percent int `json:"percent"`
	// The soonest expiry among what is left; zero if none of it expires.
	ExpiresAt int64    `json:"expires_at"`
	ModelIDs  []string `json:"model_ids"`
	Exhausted bool     `json:"exhausted"`
	// A shared pool's own figures; nil on every other kind.
	Pool *PoolView `json:"pool,omitempty"`
}

// PoolView is what an account is told about a pool beyond its balance.
type PoolView struct {
	// Only is a pool that is the one way to pay for its models.
	Only bool `json:"only"`
	// Zero is no daily limit.
	DailyLimit float64 `json:"daily_limit"`
	DailyUsed  float64 `json:"daily_used"`
	// When the balance is next put back; zero when it never is.
	ResetsAt int64 `json:"resets_at"`
}

// ViewFor is the bars an account holds something unexpired in, for the usage
// screen.
func (s *Store) ViewFor(ctx context.Context, userID string) ([]View, error) {
	now := time.Now().UnixMilli()
	rows, err := s.db.Query(ctx, `SELECT `+prefixed("b", barColumns)+`, c.enabled,
		g.amount, g.used, g.expires_at
		FROM bonus_grants g
		JOIN bonus_bars b ON b.id = g.bar_id
		LEFT JOIN bonus_choices c ON c.user_id = g.user_id AND c.bar_id = g.bar_id
		WHERE g.user_id = ? AND b.active = ? AND (g.expires_at = 0 OR g.expires_at > ?)
		ORDER BY b.created_at, b.id`, userID, true, now)
	if err != nil {
		return nil, fmt.Errorf("bonus: view: %w", err)
	}
	defer rows.Close()

	type acc struct {
		view      View
		total     float64
		remaining float64
	}
	var order []string
	byBar := map[string]*acc{}
	for rows.Next() {
		var (
			b       Bar
			models  string
			enabled *bool
			amount  float64
			used    float64
			expires int64
		)
		if err := rows.Scan(&b.ID, &b.Name, &b.Description, &b.Kind, &b.ToggleMode, &b.DefaultOn, &b.ShowTotal,
			&models, &b.DefaultExpiresAt, &b.Active, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt,
			&enabled, &amount, &used, &expires); err != nil {
			return nil, fmt.Errorf("bonus: scan view: %w", err)
		}
		a := byBar[b.ID]
		if a == nil {
			on := switchedOn(b.ToggleMode, b.DefaultOn, enabled)
			a = &acc{view: View{
				BarID: b.ID, Name: b.Name, Description: b.Description, Kind: b.Kind, ToggleMode: b.ToggleMode,
				Choosable: b.Kind == KindBonus && b.ToggleMode == ModeUser,
				Enabled:   b.Kind == KindBonus && on,
				ShowTotal: b.ShowTotal, ModelIDs: modelsForScreen(models),
			}}
			byBar[b.ID] = a
			order = append(order, b.ID)
		}
		a.total += amount
		left := math.Max(amount-used, 0)
		a.remaining += left
		if left > epsilon && expires != 0 && (a.view.ExpiresAt == 0 || expires < a.view.ExpiresAt) {
			a.view.ExpiresAt = expires
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	// The pools lead: they are everybody's, and what the account holds itself
	// follows in the order it was made.
	out, err := s.poolViews(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, barID := range order {
		a := byBar[barID]
		v := a.view
		v.Exhausted = a.remaining <= epsilon
		if a.total > 0 {
			v.Percent = int(math.Round(a.remaining / a.total * 100))
		}
		if v.ShowTotal {
			total, remaining := round(a.total), round(a.remaining)
			v.Total, v.Remaining = &total, &remaining
		}
		out = append(out, v)
	}
	return out, nil
}

// poolViews is every switched-on pool, as the account sees it. Everybody may
// draw on a pool, so every account is shown it, holding a grant or not.
func (s *Store) poolViews(ctx context.Context, userID string) ([]View, error) {
	now := time.Now()
	rows, err := s.db.Query(ctx, `SELECT `+prefixed("b", barColumns)+`, c.enabled
		FROM bonus_bars b
		LEFT JOIN bonus_choices c ON c.user_id = ? AND c.bar_id = b.id
		WHERE b.kind = ? AND b.active = ?
		ORDER BY b.created_at, b.id`, userID, KindShared, true)
	if err != nil {
		return nil, fmt.Errorf("bonus: view pools: %w", err)
	}
	defer rows.Close()
	var bars []Bar
	choices := map[string]*bool{}
	for rows.Next() {
		var (
			b       Bar
			models  string
			enabled *bool
		)
		if err := rows.Scan(&b.ID, &b.Name, &b.Description, &b.Kind, &b.ToggleMode, &b.DefaultOn, &b.ShowTotal,
			&models, &b.DefaultExpiresAt, &b.Active, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt, &enabled); err != nil {
			return nil, fmt.Errorf("bonus: scan pool view: %w", err)
		}
		b.ModelIDs = modelsForScreen(models)
		bars = append(bars, b)
		choices[b.ID] = enabled
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	out := []View{}
	if len(bars) == 0 {
		return out, nil
	}
	pools, err := s.pools(ctx)
	if err != nil {
		return nil, err
	}
	drawn, err := s.drawnToday(ctx, userID, now)
	if err != nil {
		return nil, err
	}
	loc := s.loc()
	for _, b := range bars {
		p := pools[b.ID]
		on := b.ToggleMode == ModeOnly || switchedOn(b.ToggleMode, b.DefaultOn, choices[b.ID])
		v := View{
			BarID: b.ID, Name: b.Name, Description: b.Description, Kind: b.Kind, ToggleMode: b.ToggleMode,
			Choosable: b.ToggleMode == ModeUser, Enabled: on, ShowTotal: b.ShowTotal, ModelIDs: b.ModelIDs,
			Exhausted: p.Remaining() <= epsilon,
			Pool: &PoolView{
				Only: b.ToggleMode == ModeOnly, DailyLimit: p.PerUserDaily, DailyUsed: round(drawn[b.ID]),
				ResetsAt: nextPeriod(p.ResetPeriod, p.PeriodStart, loc),
			},
		}
		if p.Amount > 0 {
			v.Percent = int(math.Round(p.Remaining() / p.Amount * 100))
		}
		if v.ShowTotal {
			total, remaining := round(p.Amount), round(p.Remaining())
			v.Total, v.Remaining = &total, &remaining
		}
		out = append(out, v)
	}
	return out, nil
}

func round(v float64) float64 { return math.Round(v*1e6) / 1e6 }
