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
			on := b.ToggleMode == ModeOn
			if b.ToggleMode == ModeUser {
				on = b.DefaultOn
				if enabled != nil {
					on = *enabled
				}
			}
			a = &acc{view: View{
				BarID: b.ID, Name: b.Name, Description: b.Description, Kind: b.Kind, ToggleMode: b.ToggleMode,
				Choosable: b.Kind == KindBonus && b.ToggleMode == ModeUser,
				Enabled:   b.Kind == KindBonus && on,
				ShowTotal: b.ShowTotal, ModelIDs: parseModels(models),
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
	out := make([]View, 0, len(order))
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

func round(v float64) float64 { return math.Round(v*1e6) / 1e6 }
