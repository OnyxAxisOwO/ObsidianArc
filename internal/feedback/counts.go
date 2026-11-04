package feedback

import (
	"context"
	"fmt"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// UnreadFor is the dot on one reader's account menu: how many of their own
// reports have an answer they have not opened yet.
func (s *Store) UnreadFor(ctx context.Context, q database.Queryer, userID string) (int, error) {
	var count int
	err := s.pick(q).QueryRow(ctx,
		`SELECT COUNT(*) FROM feedback WHERE user_id = ? AND author_unread = ?`,
		userID, true).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("feedback: unread: %w", err)
	}
	return count, nil
}

// Summary is the count block above the operator's list: how much there is,
// and how much of it is still waiting.
type Summary struct {
	Total    int `json:"total"`
	Open     int `json:"open"`
	Bugs     int `json:"bugs"`
	Ideas    int `json:"ideas"`
	HighOpen int `json:"high_open"`
	// Threads whose last word is the reader's. The one number on this screen
	// that is about the operator rather than about the instance.
	Awaiting int `json:"awaiting"`
}

// Counts answers the summary in one round trip. SUM(CASE …) rather than
// COUNT(…) FILTER: both engines here understand the first spelling, and the
// second is a newer standard than the oldest SQLite this is expected to run
// against. COALESCE because SUM over no rows is NULL on both of them, and a
// fresh instance has no rows.
func (s *Store) Counts(ctx context.Context, q database.Queryer) (Summary, error) {
	var out Summary
	err := s.pick(q).QueryRow(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN kind = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN kind = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = ? AND priority = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN operator_unread = ? THEN 1 ELSE 0 END), 0)
		FROM feedback`,
		string(StatusOpen), string(KindBug), string(KindIdea),
		string(StatusOpen), string(PriorityHigh), true).
		Scan(&out.Total, &out.Open, &out.Bugs, &out.Ideas, &out.HighOpen, &out.Awaiting)
	if err != nil {
		return Summary{}, fmt.Errorf("feedback: counts: %w", err)
	}
	return out, nil
}
