package user

import (
	"context"
	"fmt"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// ResolveMembership also serves the session join: permanent memberships keep
// their single-query lookup, while an expired grant is retired before any
// model, quota or API permission can be read from the account.
func (s *Store) ResolveMembership(ctx context.Context, q database.Queryer, record User) (User, error) {
	now := time.Now()
	if record.GroupExpiresAt == 0 || record.GroupExpiresAt > now.UnixMilli() {
		return record, nil
	}
	if q == nil {
		q = s.db
	}
	if err := s.expireMemberships(ctx, q, now, record.ID); err != nil {
		return User{}, err
	}
	// The conditional write may have lost to a renewal; read the winning row
	// instead of returning a default group assembled from the stale snapshot.
	return scanUser(q.QueryRow(ctx, `SELECT `+columnList()+` FROM users WHERE id = ?`, record.ID))
}

func (s *Store) ExpireMemberships(ctx context.Context, q database.Queryer, now time.Time) error {
	if q == nil {
		q = s.db
	}
	return s.expireMemberships(ctx, q, now, "")
}

func (s *Store) expireMemberships(ctx context.Context, q database.Queryer, now time.Time, userID string) error {
	// One conditional UPDATE holds the row lock through the decision and write
	// on both engines. A concurrent renewal cannot be undone by a stale sweep.
	// The ordering matches group.Store.Default, including its legacy fallback
	// when no group has been marked as the default.
	query := `UPDATE users SET group_id = (
		SELECT id FROM user_groups ORDER BY is_default DESC, sort_order, id LIMIT 1
	), group_expires_at = 0, updated_at = ?
	WHERE group_expires_at > 0 AND group_expires_at <= ?`
	args := []any{now.UnixMilli(), now.UnixMilli()}
	if userID != "" {
		query += ` AND id = ?`
		args = append(args, userID)
	}
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("user: expire memberships: %w", err)
	}
	return nil
}
