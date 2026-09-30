package bonus

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// Target is who a grant goes to: everyone, a group, or named accounts.
type Target struct {
	All     bool
	GroupID string
	UserIDs []string
}

// Result is what a grant did.
type Result struct {
	Count int `json:"count"`
}

func checkAmount(amount float64) error {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 || amount > MaxAmount {
		return ErrInvalidAmount
	}
	return nil
}

func checkExpiry(expiresAt int64, now int64) error {
	if expiresAt != 0 && expiresAt <= now {
		return ErrInvalidExpiry
	}
	return nil
}

// Grant gives amount credits in a bar to every account the target names,
// all or nothing. expiresAt zero means it never expires; a person who did not
// choose one passes the bar's default (see DefaultExpiry).
func (s *Store) Grant(ctx context.Context, barID string, target Target, amount float64,
	expiresAt int64, source, note, by string) (Result, error) {
	now := time.Now().UnixMilli()
	if err := checkAmount(amount); err != nil {
		return Result{}, err
	}
	if err := checkExpiry(expiresAt, now); err != nil {
		return Result{}, err
	}
	note = truncate(strings.TrimSpace(note), maxNote)

	var count int
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := s.Bar(ctx, tx, barID); err != nil {
			return err
		}
		ids, err := s.recipients(ctx, tx, target)
		if err != nil {
			return err
		}
		for _, userID := range ids {
			if err := insertGrant(ctx, tx, Grant{
				BarID: barID, UserID: userID, Amount: amount, ExpiresAt: expiresAt,
				Source: source, Note: note, GrantedBy: by, CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		count = len(ids)
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Count: count}, nil
}

// GrantTo is one grant to one account inside the caller's transaction, for
// the rewards that are granted together with something else — a check-in.
func (s *Store) GrantTo(ctx context.Context, q database.Queryer, barID, userID string, amount float64,
	expiresAt int64, source, note string) (Grant, error) {
	now := time.Now().UnixMilli()
	if err := checkAmount(amount); err != nil {
		return Grant{}, err
	}
	if err := checkExpiry(expiresAt, now); err != nil {
		return Grant{}, err
	}
	bar, err := s.Bar(ctx, q, barID)
	if err != nil {
		return Grant{}, err
	}
	if !bar.Active {
		return Grant{}, ErrNotFound
	}
	g := Grant{BarID: barID, UserID: userID, Amount: amount, ExpiresAt: expiresAt,
		Source: source, Note: truncate(strings.TrimSpace(note), maxNote), CreatedAt: now}
	if err := insertGrant(ctx, q, g); err != nil {
		return Grant{}, err
	}
	return g, nil
}

func insertGrant(ctx context.Context, q database.Queryer, g Grant) error {
	g.ID = id.New()
	if _, err := q.Exec(ctx, `INSERT INTO bonus_grants
		(id, bar_id, user_id, amount, used, expires_at, source, note, granted_by, created_at, warned_at)
		VALUES (?, ?, ?, ?, 0, ?, ?, ?, ?, ?, 0)`,
		g.ID, g.BarID, g.UserID, g.Amount, g.ExpiresAt, g.Source, g.Note, g.GrantedBy, g.CreatedAt); err != nil {
		return fmt.Errorf("bonus: insert grant: %w", err)
	}
	return nil
}

func (s *Store) recipients(ctx context.Context, q database.Queryer, t Target) ([]string, error) {
	var (
		rows *sql.Rows
		err  error
	)
	switch {
	case t.All:
		rows, err = q.Query(ctx, `SELECT id FROM users ORDER BY id`)
	case t.GroupID != "":
		rows, err = q.Query(ctx, `SELECT id FROM users WHERE group_id = ? ORDER BY id`, t.GroupID)
	case len(t.UserIDs) > 0:
		seen := map[string]bool{}
		out := make([]string, 0, len(t.UserIDs))
		for _, userID := range t.UserIDs {
			if userID = strings.TrimSpace(userID); userID == "" || seen[userID] {
				continue
			}
			seen[userID] = true
			var found string
			if err := q.QueryRow(ctx, `SELECT id FROM users WHERE id = ?`, userID).Scan(&found); err != nil {
				if database.IsNotFound(err) {
					return nil, fmt.Errorf("%w: no account %s", ErrNotFound, userID)
				}
				return nil, err
			}
			out = append(out, found)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: a grant needs somebody to go to", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("bonus: list recipients: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		out = append(out, userID)
	}
	return out, rows.Err()
}

// GrantRow is a grant with the name of whoever holds it, for the
// administrator's list.
type GrantRow struct {
	Grant
	Username string `json:"username"`
}

// GrantsInBar pages the grants in a bar, newest first.
func (s *Store) GrantsInBar(ctx context.Context, barID string, limit, offset int) ([]GrantRow, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM bonus_grants WHERE bar_id = ?`, barID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("bonus: count grants: %w", err)
	}
	rows, err := s.db.Query(ctx, `SELECT g.id, g.bar_id, g.user_id, g.amount, g.used, g.expires_at, g.source,
		g.note, g.granted_by, g.created_at, g.warned_at, u.username
		FROM bonus_grants g JOIN users u ON u.id = g.user_id
		WHERE g.bar_id = ? ORDER BY g.created_at DESC, g.id DESC LIMIT ? OFFSET ?`, barID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("bonus: list grants: %w", err)
	}
	defer rows.Close()
	out := []GrantRow{}
	for rows.Next() {
		var r GrantRow
		if err := rows.Scan(&r.ID, &r.BarID, &r.UserID, &r.Amount, &r.Used, &r.ExpiresAt, &r.Source,
			&r.Note, &r.GrantedBy, &r.CreatedAt, &r.WarnedAt, &r.Username); err != nil {
			return nil, 0, fmt.Errorf("bonus: scan grant: %w", err)
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// Revoke takes back what is left of a grant. The row stays, with its amount
// cut to what was spent, so the account's history still says what happened.
func (s *Store) Revoke(ctx context.Context, grantID string) (float64, error) {
	var revoked float64
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		var g Grant
		if err := tx.QueryRow(ctx, `SELECT amount, used FROM bonus_grants WHERE id = ?`, grantID).Scan(&g.Amount, &g.Used); err != nil {
			if database.IsNotFound(err) {
				return ErrNotFound
			}
			return err
		}
		revoked = g.Remaining()
		_, err := tx.Exec(ctx, `UPDATE bonus_grants SET amount = used WHERE id = ?`, grantID)
		return err
	})
	return revoked, err
}

// SetChoice records whether an account wants a 'user' bar spent first.
func (s *Store) SetChoice(ctx context.Context, userID, barID string, enabled bool) error {
	bar, err := s.Bar(ctx, nil, barID)
	if err != nil {
		return err
	}
	if !bar.Active || bar.Kind != KindBonus || bar.ToggleMode != ModeUser {
		return ErrNotChoosable
	}
	var held int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM bonus_grants WHERE bar_id = ? AND user_id = ?`, barID, userID).Scan(&held); err != nil {
		return err
	}
	if held == 0 {
		return ErrNotFound
	}
	_, err = s.db.Exec(ctx, `INSERT INTO bonus_choices (user_id, bar_id, enabled, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, bar_id) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`,
		userID, barID, enabled, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("bonus: save choice: %w", err)
	}
	return nil
}
