package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// An account's address changes are metered, because each address it moves to
// is a paid lookup that its own password is the only thing paying for.
const (
	emailScreensPerHour = 5
	emailScreenWindow   = time.Hour
)

// CheckRegistrationEmail is the network preflight shared by password and
// provider sign-ups. Keeping it outside Provision's transaction prevents a
// slow third-party request from holding the instance registration lock.
func (s *Service) CheckRegistrationEmail(ctx context.Context, email string) error {
	if s.ScreenEmail == nil || email == "" {
		return nil
	}
	return s.ScreenEmail(ctx, email)
}

// reserveEmailScreen spends one of the account's places in the hourly allowance
// before the paid lookup runs, so a refused change costs the provider nothing.
// It is its own short transaction: the lookup happens outside every transaction,
// and the place has to be held before it starts.
func (s *Service) reserveEmailScreen(ctx context.Context, userID string) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
			return fmt.Errorf("auth: lock account for screening: %w", err)
		}
		now := time.Now()
		since := now.Add(-emailScreenWindow).UnixMilli()
		var used int
		var oldest int64
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*), COALESCE(MIN(screened_at), 0) FROM email_change_screens
			 WHERE user_id = ? AND screened_at > ?`, userID, since).Scan(&used, &oldest); err != nil {
			return fmt.Errorf("auth: count address screens: %w", err)
		}
		if used >= emailScreensPerHour {
			// The earliest place to free up is the oldest one leaving the window.
			// Telling the caller that wait is a lower bound, since several may
			// need to leave before a change is allowed, and the caller can ask again.
			return &RateLimitError{RetryAfter: time.Until(time.UnixMilli(oldest).Add(emailScreenWindow))}
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM email_change_screens WHERE user_id = ? AND screened_at <= ?`, userID, since); err != nil {
			return fmt.Errorf("auth: prune address screens: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO email_change_screens (user_id, screened_at) VALUES (?, ?)`, userID, now.UnixMilli()); err != nil {
			return fmt.Errorf("auth: record address screen: %w", err)
		}
		return nil
	})
}
