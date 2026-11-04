package backup

import (
	"context"
	"fmt"
	"unicode/utf8"
)

// ceiling is how many messages one account may store. Zero configures the
// package default.
func (s *Service) ceiling() int {
	if s.MaxStoredMessages > 0 {
		return s.MaxStoredMessages
	}
	return MaxStoredMessages
}

func (s *Service) charCeiling() int64 {
	if s.MaxStoredChars > 0 {
		return s.MaxStoredChars
	}
	return MaxStoredChars
}

// storedChars is the text an account holds now, in characters: message content,
// reasoning and error, and conversation titles. It is every column an import
// writes, so the ceiling counts what the import adds to.
//
// It is counted in Go rather than with SUM(LENGTH(...)). SQLite's LENGTH stops
// at the first NUL, so text carrying one counts for almost nothing and an
// account could fill itself past the ceiling. Reading every row is the cost of
// an exact count; it is paid once per import, and the rows are read one at a
// time, so only one message is held in memory however large the account.
func (s *Service) storedChars(ctx context.Context, userID string) (int64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT content, reasoning, error FROM messages WHERE user_id = ?`, userID)
	if err != nil {
		return 0, fmt.Errorf("backup: measure stored text: %w", err)
	}
	defer rows.Close()

	var chars int64
	for rows.Next() {
		var content, reasoning, errorText string
		if err := rows.Scan(&content, &reasoning, &errorText); err != nil {
			return 0, fmt.Errorf("backup: measure stored text: %w", err)
		}
		chars += int64(utf8.RuneCountInString(content) + utf8.RuneCountInString(reasoning) +
			utf8.RuneCountInString(errorText))
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("backup: measure stored text: %w", err)
	}
	rows.Close()

	titles, err := s.db.Query(ctx,
		`SELECT title FROM conversations WHERE user_id = ?`, userID)
	if err != nil {
		return 0, fmt.Errorf("backup: measure stored titles: %w", err)
	}
	defer titles.Close()
	for titles.Next() {
		var title string
		if err := titles.Scan(&title); err != nil {
			return 0, fmt.Errorf("backup: measure stored titles: %w", err)
		}
		chars += int64(utf8.RuneCountInString(title))
	}
	if err := titles.Err(); err != nil {
		return 0, fmt.Errorf("backup: measure stored titles: %w", err)
	}
	return chars, nil
}

// charTally is one import's running view of the character ceiling: what the
// account held when the import started, plus what its own threads have added.
type charTally struct {
	ceiling int64
	stored  int64
}
