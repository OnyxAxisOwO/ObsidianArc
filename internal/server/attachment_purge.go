package server

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// purgeClaim is what one instance decided about today's daily purge, and
// what it wrote down to say so.
type purgeClaim struct {
	decision conversation.Decision
	// The stored time before this claim, which is what the sweep compares
	// against and what a failed run puts back.
	previous int64
	// The value this claim wrote, or "" when it wrote nothing.
	written string
}

// claimDailyPurge decides whether today's purge is owed and, when it is or
// when the schedule has never run, records the time in the same transaction.
//
// It is a check-then-write that more than one instance can make at once, so
// it holds the instance lock across both and reads the stored time from the
// database: the settings cache is filled at boot and updated only by this
// process's own writes, so another instance's run never reaches it, and
// deciding from it would purge once per instance per day — the second one
// destroying whatever was uploaded in the minutes between.
//
// The purge itself runs after the commit. It is one UPDATE over every
// attachment, and holding the instance lock across it would queue
// registrations and demotions behind a cleanup.
func (s *Server) claimDailyPurge(ctx context.Context, dailyAt string, now time.Time) (purgeClaim, error) {
	// Nothing configured is decided without the lock, so an instance with no
	// daily purge never takes the row on its ten-minute tick.
	if _, _, ok := conversation.ParseDailyTime(dailyAt); !ok {
		return purgeClaim{decision: conversation.DecisionSkip}, nil
	}

	var claim purgeClaim
	written := strconv.FormatInt(now.UnixMilli(), 10)
	err := s.deps.DB.Tx(ctx, func(tx *database.Tx) error {
		claim = purgeClaim{}
		if err := settings.Lock(ctx, tx); err != nil {
			return err
		}
		var stored string
		err := tx.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`,
			settings.AttachmentPurgeLast).Scan(&stored)
		if err != nil && !errors.Is(err, database.ErrNoRows) {
			return err
		}
		// A value that does not parse is treated as never run, which is what
		// the cached read this replaced did with it.
		previous, _ := strconv.ParseInt(strings.TrimSpace(stored), 10, 64)
		claim.previous = previous
		claim.decision = conversation.DecidePurge(dailyAt, now, previous)
		if claim.decision == conversation.DecisionSkip {
			return nil
		}
		claim.written = written
		// WriteMany rather than Set: Set takes the settings write mutex, which
		// must not be waited on while the instance lock is held.
		return s.settings.WriteMany(ctx, tx, map[string]string{settings.AttachmentPurgeLast: written})
	})
	if err != nil {
		return purgeClaim{decision: conversation.DecisionSkip}, err
	}
	if claim.written != "" {
		s.settings.Remember(map[string]string{settings.AttachmentPurgeLast: claim.written})
	}
	return claim, nil
}

// releaseDailyPurge puts back the time a claim replaced, so a purge that was
// claimed and then failed is owed again rather than skipped until tomorrow.
// It only replaces its own claim: a value somebody else has written since — an
// administrator's purge, another instance's run — is newer than this one and
// stays.
func (s *Server) releaseDailyPurge(ctx context.Context, claim purgeClaim) {
	if claim.written == "" {
		return
	}
	previous := strconv.FormatInt(claim.previous, 10)
	if _, err := s.deps.DB.Exec(ctx,
		`UPDATE settings SET value = ?, updated_at = ? WHERE key = ? AND value = ?`,
		previous, time.Now().UnixMilli(), settings.AttachmentPurgeLast, claim.written); err != nil {
		slog.ErrorContext(ctx, "could not release the attachment purge claim", "error", err)
		return
	}
	s.settings.Remember(map[string]string{settings.AttachmentPurgeLast: previous})
}
