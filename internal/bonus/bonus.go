// Package bonus holds the extra allowances an administrator adds beside the
// three quota windows: bars, the grants each account holds in them, and the
// rules for spending them. docs/architecture/bonus-and-checkin.md is the
// statement of those rules; this is the code that keeps them.
//
// The unit is credits, the same one model prices and the windows' credit
// limits are written in, so a grant needs no conversion to be spent.
package bonus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

type Kind string

const (
	// KindBonus is spent when it is switched on.
	KindBonus Kind = "bonus"
	// KindReserve is spent only after everything else is, and has no switch.
	KindReserve Kind = "reserve"
)

type Mode string

const (
	// ModeUser lets the account choose.
	ModeUser Mode = "user"
	// ModeOn spends the bar first whatever the account wants.
	ModeOn Mode = "on"
	// ModeOff keeps the bar for when everything else is spent.
	ModeOff Mode = "off"
)

// Bar is the rules; what an account holds in it is Grants.
type Bar struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Kind        Kind     `json:"kind"`
	ToggleMode  Mode     `json:"toggle_mode"`
	DefaultOn   bool     `json:"default_on"`
	ShowTotal   bool     `json:"show_total"`
	ModelIDs    []string `json:"model_ids"`
	// Zero means a grant that does not choose an expiry does not expire.
	DefaultExpiresAt int64  `json:"default_expires_at"`
	Active           bool   `json:"active"`
	CreatedBy        string `json:"created_by"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

// covers reports whether a request for modelID may spend this bar.
func (b Bar) covers(modelID string) bool {
	if len(b.ModelIDs) == 0 {
		return true
	}
	for _, id := range b.ModelIDs {
		if id == modelID {
			return true
		}
	}
	return false
}

// Grant is one amount an account holds in a bar.
type Grant struct {
	ID        string  `json:"id"`
	BarID     string  `json:"bar_id"`
	UserID    string  `json:"user_id"`
	Amount    float64 `json:"amount"`
	Used      float64 `json:"used"`
	ExpiresAt int64   `json:"expires_at"`
	Source    string  `json:"source"`
	Note      string  `json:"note"`
	GrantedBy string  `json:"granted_by"`
	CreatedAt int64   `json:"created_at"`
	WarnedAt  int64   `json:"warned_at"`
}

// Remaining is what is left of the grant, ignoring its expiry.
func (g Grant) Remaining() float64 {
	if left := g.Amount - g.Used; left > 0 {
		return left
	}
	return 0
}

var (
	ErrNotFound      = errors.New("bonus: not found")
	ErrInvalidBar    = errors.New("bonus: a bar needs a name, a kind and a switch mode it knows")
	ErrInvalidAmount = errors.New("bonus: an amount is more than zero and at most a million")
	ErrInvalidExpiry = errors.New("bonus: an expiry is in the future, or zero for none")
	// ErrNotChoosable is a switch pressed on a bar the account has no say in.
	ErrNotChoosable = errors.New("bonus: that bar is not the account's to switch")
	// ErrInsufficient is a reservation the bar could not cover whole.
	ErrInsufficient = errors.New("bonus: not enough to cover it")
	// ErrBarOff is a grant into a bar whose switch is off. It wraps ErrNotFound
	// because the check-in and plugin paths have always reported a bar that
	// cannot be granted into as not found; TranslateError checks for it first so
	// the administrator is told which it was.
	ErrBarOff = fmt.Errorf("%w: that bar is switched off", ErrNotFound)
	// ErrNoAccount is a recipient named by a username or ID nobody holds. It
	// wraps ErrNotFound as the other lookups do; TranslateError checks for it
	// first, so the administrator hears that the name is wrong and not that the
	// bonus is missing.
	ErrNoAccount = fmt.Errorf("%w: no account has that name or ID", ErrNotFound)
	// ErrModelsUnreadable is a save over a bar whose stored model list does not
	// parse. It is refused rather than written, so the restriction stays as it is
	// in the database instead of being replaced by the empty list the screen
	// shows for it.
	ErrModelsUnreadable = errors.New("bonus: a bar's stored model list cannot be read")
)

// Limits on what one grant may be: nothing legitimate is beyond them, and a
// typo that adds three zeros is caught rather than granted to everyone.
const (
	MaxAmount = 1_000_000
	// A bar's name and description are shown to accounts.
	maxName = 60
	maxText = 500
	maxNote = 200
)

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

const barColumns = `id, name, description, kind, toggle_mode, default_on, show_total, model_ids,
	default_expires_at, active, created_by, created_at, updated_at`

type scanner interface{ Scan(dest ...any) error }

func scanBar(row scanner) (Bar, error) {
	var (
		b      Bar
		models string
	)
	err := row.Scan(&b.ID, &b.Name, &b.Description, &b.Kind, &b.ToggleMode, &b.DefaultOn, &b.ShowTotal,
		&models, &b.DefaultExpiresAt, &b.Active, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Bar{}, ErrNotFound
		}
		return Bar{}, fmt.Errorf("bonus: scan bar: %w", err)
	}
	b.ModelIDs = modelsForScreen(models)
	return b, nil
}

// parseModels reads a bar's stored model list. An empty cell is a bar that
// covers every model. A cell that does not parse is an error and not a bar
// that covers every model: covers reads an empty list as every model, so a
// damaged restriction must not come back as no restriction at all.
func parseModels(raw string) ([]string, error) {
	out := []string{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("bonus: model list: %w", err)
	}
	if out == nil {
		return nil, errors.New("bonus: model list is null")
	}
	return out, nil
}

// modelsForScreen is parseModels for the screens that list bars. A damaged row
// is logged and shown with no model list, which reads as every model: the
// screen is wrong about that bar, but failing the whole list over one row is
// worse. Spending does not use this; candidates refuses such a grant instead.
func modelsForScreen(raw string) []string {
	ids, err := parseModels(raw)
	if err != nil {
		slog.Error("bonus: a bar's model list cannot be read", "error", err)
		return []string{}
	}
	return ids
}

func encodeModels(ids []string) string {
	clean := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			clean = append(clean, id)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	raw, _ := json.Marshal(clean)
	return string(raw)
}

func (b *Bar) normalise() error {
	b.Name = truncate(strings.TrimSpace(b.Name), maxName)
	b.Description = truncate(strings.TrimSpace(b.Description), maxText)
	if b.Name == "" {
		return ErrInvalidBar
	}
	if b.Kind == "" {
		b.Kind = KindBonus
	}
	if b.ToggleMode == "" {
		b.ToggleMode = ModeUser
	}
	if b.Kind != KindBonus && b.Kind != KindReserve {
		return ErrInvalidBar
	}
	switch b.ToggleMode {
	case ModeUser, ModeOn, ModeOff:
	default:
		return ErrInvalidBar
	}
	if b.Kind == KindReserve {
		// Nothing to choose: it is the last thing spent, and always.
		b.ToggleMode = ModeOff
	}
	if b.DefaultExpiresAt < 0 {
		return ErrInvalidExpiry
	}
	return nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// CreateBar adds a bar.
func (s *Store) CreateBar(ctx context.Context, bar Bar, createdBy string) (Bar, error) {
	if err := bar.normalise(); err != nil {
		return Bar{}, err
	}
	now := time.Now().UnixMilli()
	bar.ID, bar.CreatedBy, bar.CreatedAt, bar.UpdatedAt = id.New(), createdBy, now, now
	bar.Active = true
	if _, err := s.db.Exec(ctx, `INSERT INTO bonus_bars (`+barColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		bar.ID, bar.Name, bar.Description, bar.Kind, bar.ToggleMode, bar.DefaultOn, bar.ShowTotal,
		encodeModels(bar.ModelIDs), bar.DefaultExpiresAt, bar.Active, bar.CreatedBy, bar.CreatedAt, bar.UpdatedAt); err != nil {
		return Bar{}, fmt.Errorf("bonus: create bar: %w", err)
	}
	return s.Bar(ctx, nil, bar.ID)
}

// UpdateBar replaces a bar's rules. What was granted is untouched: a rule
// change reaches everyone at once, an amount does not. A caller that read the
// bar to decide this write passes its transaction, so the read and the write
// are one unit behind the row lock it took first.
func (s *Store) UpdateBar(ctx context.Context, q database.Queryer, bar Bar) (Bar, error) {
	if q == nil {
		q = s.db
	}
	if err := bar.normalise(); err != nil {
		return Bar{}, err
	}
	// The stored cell is read as written, not through modelsForScreen. The screen
	// shows a damaged list as no list, which reads as every model, and the form
	// sends that back: saving over it would make a restricted bar spendable on
	// every model. Refusing leaves the damage where the operator can see it.
	var raw string
	if err := q.QueryRow(ctx, `SELECT model_ids FROM bonus_bars WHERE id = ?`, bar.ID).Scan(&raw); err != nil {
		if database.IsNotFound(err) {
			return Bar{}, ErrNotFound
		}
		return Bar{}, fmt.Errorf("bonus: read bar: %w", err)
	}
	if _, err := parseModels(raw); err != nil {
		return Bar{}, fmt.Errorf("%w: %w", ErrModelsUnreadable, err)
	}
	res, err := q.Exec(ctx, `UPDATE bonus_bars SET name = ?, description = ?, kind = ?, toggle_mode = ?,
		default_on = ?, show_total = ?, model_ids = ?, default_expires_at = ?, active = ?, updated_at = ?
		WHERE id = ?`,
		bar.Name, bar.Description, bar.Kind, bar.ToggleMode, bar.DefaultOn, bar.ShowTotal,
		encodeModels(bar.ModelIDs), bar.DefaultExpiresAt, bar.Active, time.Now().UnixMilli(), bar.ID)
	if err != nil {
		return Bar{}, fmt.Errorf("bonus: update bar: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Bar{}, ErrNotFound
	}
	return s.Bar(ctx, q, bar.ID)
}

// DeleteBar removes a bar and every grant in it. A caller that checked the bar
// before deleting it passes its transaction, so the check and the delete are
// one unit under the lock it took first, as UpdateBar's callers do.
func (s *Store) DeleteBar(ctx context.Context, q database.Queryer, barID string) error {
	if q == nil {
		q = s.db
	}
	res, err := q.Exec(ctx, `DELETE FROM bonus_bars WHERE id = ?`, barID)
	if err != nil {
		return fmt.Errorf("bonus: delete bar: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Bar(ctx context.Context, q database.Queryer, barID string) (Bar, error) {
	if q == nil {
		q = s.db
	}
	return scanBar(q.QueryRow(ctx, `SELECT `+barColumns+` FROM bonus_bars WHERE id = ?`, barID))
}

// Bars lists every bar, newest first, with how much has been granted and
// spent in each.
func (s *Store) Bars(ctx context.Context) ([]BarSummary, error) {
	rows, err := s.db.Query(ctx, `SELECT `+prefixed("b", barColumns)+`,
		COALESCE(SUM(g.amount), 0), COALESCE(SUM(g.used), 0), COUNT(DISTINCT g.user_id)
		FROM bonus_bars b LEFT JOIN bonus_grants g ON g.bar_id = b.id
		GROUP BY `+prefixed("b", barColumns)+`
		ORDER BY b.created_at DESC, b.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("bonus: list bars: %w", err)
	}
	defer rows.Close()
	out := []BarSummary{}
	for rows.Next() {
		var (
			s      BarSummary
			models string
		)
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Kind, &s.ToggleMode, &s.DefaultOn, &s.ShowTotal,
			&models, &s.DefaultExpiresAt, &s.Active, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt,
			&s.Granted, &s.Used, &s.Holders); err != nil {
			return nil, fmt.Errorf("bonus: scan bar: %w", err)
		}
		s.ModelIDs = modelsForScreen(models)
		out = append(out, s)
	}
	return out, rows.Err()
}

// BarSummary is a bar with its totals, for the administrator's list.
type BarSummary struct {
	Bar
	Granted float64 `json:"granted"`
	Used    float64 `json:"used"`
	Holders int     `json:"holders"`
}

func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}
