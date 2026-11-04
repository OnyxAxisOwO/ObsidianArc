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
	// Names, as a person types them; case does not matter.
	Usernames []string
}

// Result is what a grant did. UserIDs is exactly who received a grant, so the
// notices that follow a commit can go to that set rather than to a second
// reading of the group or the names, which may have changed since.
type Result struct {
	Count   int      `json:"count"`
	UserIDs []string `json:"-"`
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

	var (
		count   int
		granted []string
	)
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// Lock the bar before reading its switch. Switching it off writes this
		// row, and on PostgreSQL's READ COMMITTED a read taken first can still
		// see it on while the switch-off commits; the grant would then be
		// written into a bar that is off.
		if _, err := tx.Exec(ctx, `UPDATE bonus_bars SET updated_at = updated_at WHERE id = ?`, barID); err != nil {
			return err
		}
		bar, err := s.Bar(ctx, tx, barID)
		if err != nil {
			return err
		}
		// A switched-off bar is not spent, so a grant into it would be stored,
		// reported and announced, and never usable. GrantTo refuses it too.
		if !bar.Active {
			return ErrBarOff
		}
		if bar.Kind == KindShared {
			return ErrSharedNoGrants
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
		granted = ids
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Count: count, UserIDs: granted}, nil
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
	// The same lock as Grant, taken inside the caller's transaction: the switch
	// is read under it, so a switch-off that commits meanwhile waits.
	if _, err := q.Exec(ctx, `UPDATE bonus_bars SET updated_at = updated_at WHERE id = ?`, barID); err != nil {
		return Grant{}, err
	}
	bar, err := s.Bar(ctx, q, barID)
	if err != nil {
		return Grant{}, err
	}
	if !bar.Active {
		return Grant{}, ErrBarOff
	}
	if bar.Kind == KindShared {
		return Grant{}, ErrSharedNoGrants
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
	case len(t.UserIDs) > 0 || len(t.Usernames) > 0:
		seen := map[string]bool{}
		var out []string
		find := func(ref, query string) error {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				return nil
			}
			var found string
			if err := q.QueryRow(ctx, query, ref).Scan(&found); err != nil {
				if database.IsNotFound(err) {
					return fmt.Errorf("%w: %s", ErrNoAccount, ref)
				}
				return err
			}
			if !seen[found] {
				seen[found] = true
				out = append(out, found)
			}
			return nil
		}
		for _, userID := range t.UserIDs {
			if err := find(userID, `SELECT id FROM users WHERE id = ?`); err != nil {
				return nil, err
			}
		}
		for _, name := range t.Usernames {
			if err := find(strings.ToLower(name), `SELECT id FROM users WHERE username_lower = ?`); err != nil {
				return nil, err
			}
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

// GrantOwner is the account a grant was made to, or ErrNotFound. It is what a
// caller deciding whether it may revoke the grant asks first, so it reads the
// row alone rather than the grant with its bar.
func (s *Store) GrantOwner(ctx context.Context, q database.Queryer, grantID string) (string, error) {
	if q == nil {
		q = s.db
	}
	var owner string
	if err := q.QueryRow(ctx, `SELECT user_id FROM bonus_grants WHERE id = ?`, grantID).Scan(&owner); err != nil {
		if database.IsNotFound(err) {
			return "", ErrNotFound
		}
		return "", err
	}
	return owner, nil
}

// Revoke takes back what is left of a grant. The row stays, with its amount
// cut to what was spent, so the account's history still says what happened.
func (s *Store) Revoke(ctx context.Context, grantID string) (float64, error) {
	var revoked float64
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// Lock before reading, as Settle does. On PostgreSQL's READ COMMITTED a
		// read taken before the lock can miss a spend that commits in between,
		// and the figure returned would then differ from what was removed.
		if _, err := tx.Exec(ctx, `UPDATE bonus_grants SET used = used WHERE id = ?`, grantID); err != nil {
			return err
		}
		var g Grant
		if err := tx.QueryRow(ctx, `SELECT amount, used FROM bonus_grants WHERE id = ?`, grantID).Scan(&g.Amount, &g.Used); err != nil {
			if database.IsNotFound(err) {
				return ErrNotFound
			}
			return err
		}
		revoked = g.Remaining()
		// The marker is set once. A grant that is already revoked has nothing
		// left to take, and the first time is the one the history should show.
		_, err := tx.Exec(ctx, `UPDATE bonus_grants SET amount = used, revoked_at = ?
			WHERE id = ? AND revoked_at = 0`, time.Now().UnixMilli(), grantID)
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
	if !bar.Active || (bar.Kind != KindBonus && bar.Kind != KindShared) || bar.ToggleMode != ModeUser {
		return ErrNotChoosable
	}
	// Everybody holds a share of a pool, so there is no grant to look for.
	var held int
	if bar.Kind == KindShared {
		held = 1
	} else if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM bonus_grants WHERE bar_id = ? AND user_id = ?`, barID, userID).Scan(&held); err != nil {
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

// Expiring is what an account is about to lose in one bar.
type Expiring struct {
	UserID    string
	BarID     string
	BarName   string
	Remaining float64
	ExpiresAt int64
}

// TakeExpiring returns what is unspent in grants that expire within the given
// time and whose holders have not been told, one entry per account and bar, and
// marks those grants as told. Marking comes first: a notice lost is better
// than the same one sent every ten minutes.
//
// The mark is a conditional write. Two sweeps against one database, which the
// deployment notes allow from two instances, can both read a grant as untold
// before either commits. The write that finds it already marked matches
// nothing, so only the sweep that makes the mark is counted and tells the
// account.
func (s *Store) TakeExpiring(ctx context.Context, within time.Duration) ([]Expiring, error) {
	now := time.Now().UnixMilli()
	until := now + within.Milliseconds()
	var out []Expiring
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		out = nil
		rows, err := tx.Query(ctx, `SELECT g.id, g.user_id, g.bar_id, b.name, g.amount - g.used, g.expires_at
			FROM bonus_grants g JOIN bonus_bars b ON b.id = g.bar_id
			WHERE b.active = ? AND g.warned_at = 0 AND g.amount - g.used > 0 AND g.expires_at > ? AND g.expires_at <= ?
			ORDER BY g.expires_at`, true, now, until)
		if err != nil {
			return fmt.Errorf("bonus: find expiring: %w", err)
		}
		type candidate struct {
			id string
			e  Expiring
		}
		var found []candidate
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.e.UserID, &c.e.BarID, &c.e.BarName, &c.e.Remaining, &c.e.ExpiresAt); err != nil {
				rows.Close()
				return err
			}
			found = append(found, c)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		index := map[string]int{}
		for _, c := range found {
			res, err := tx.Exec(ctx, `UPDATE bonus_grants SET warned_at = ? WHERE id = ? AND warned_at = 0`, now, c.id)
			if err != nil {
				return err
			}
			marked, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if marked == 0 {
				continue
			}
			// Keyed by bar ID: names are not unique, and two bars that share one
			// would otherwise be one notice with the first bar's expiry.
			key := c.e.UserID + "|" + c.e.BarID
			if i, ok := index[key]; ok {
				out[i].Remaining += c.e.Remaining
				continue
			}
			index[key] = len(out)
			out = append(out, c.e)
		}
		return nil
	})
	// A failed transaction has unmarked every grant it touched, so what it
	// collected must not reach the caller: the next sweep would find those grants
	// untold again and notify the same accounts twice.
	if err != nil {
		return nil, err
	}
	return out, nil
}
