package conversation

import "context"

func (s *Store) ListPage(ctx context.Context, userID string, limit, offset int) ([]Conversation, int, error) {
	// Counts exactly what list returns. Archived conversations are not on any
	// page, so counting them made the total run past the last page and left
	// archived rows unreachable from the admin screen that uses this.
	var total int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM conversations WHERE user_id = ? AND archived = ?`, userID, false).Scan(&total); err != nil {
		return nil, 0, err
	}
	archived := false
	rows, err := s.ListWithFilter(ctx, userID, ListFilter{Limit: limit, Offset: offset, Archived: &archived})
	return rows, total, err
}
