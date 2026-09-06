package conversation

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// How long this server holds a picture, and when it lets go of one.
//
// The default policy already drops an image the moment the turn carrying it
// has been dispatched, so on a normal instance there is nothing here to do.
// This exists for the two cases where that is not enough: an operator who
// turned retention on so a model can see an image several turns later, and
// an instance that has images from before the policy existed.
//
// Two independent rules, because they answer different questions. An age
// limit answers "how long may a picture live"; a daily purge answers "when is
// this server empty". Either can be off, and both can be on — an instance
// that wipes at 03:00 and also caps age at seven days is not contradicting
// itself, it is saying both things.

// Retention is the operator's policy, read from settings on each sweep so a
// change takes effect without a restart.
type Retention struct {
	// Bytes of an attachment older than this are dropped. Zero means age is
	// not a reason to drop anything.
	AfterDays int
	// "HH:MM" in the server's local time. Once a day at that hour every
	// stored picture is dropped, whatever its age. Empty means never.
	DailyAt string
	// How long an upload that was never sent is kept before the row goes
	// entirely. Distinct from the two above: nothing has been dispatched, so
	// there is no record worth keeping.
	OrphanTTL time.Duration
}

// SweepResult is what one pass actually did, so the janitor can log something
// truthful and the administration screen can say when it last ran.
type SweepResult struct {
	// Attachments whose bytes were dropped for being too old.
	Aged int64
	// Attachments whose bytes were dropped by the daily purge.
	Purged int64
	// Rows removed entirely: uploads that were never sent.
	Orphans  int64
	RanDaily bool
}

// Decision is what the janitor should do about the daily purge this tick.
type Decision int

const (
	// Not configured, or configured and already done for today.
	DecisionSkip Decision = iota
	// Configured but never run. Record the time and purge nothing: an
	// operator who sets a 03:00 cleanup at three in the afternoon did not ask
	// for everything to disappear right then.
	DecisionSeed
	DecisionRun
)

// DecidePurge works out whether today's purge is owed.
//
// The comparison is against today's scheduled moment rather than against a
// fixed interval, so it is correct however the janitor's ticks happen to land
// and however long the process was down. A server that was off all day and
// starts at midnight finds this morning's purge outstanding and runs it.
func DecidePurge(dailyAt string, now time.Time, lastRun int64) Decision {
	hour, minute, ok := ParseDailyTime(dailyAt)
	if !ok {
		return DecisionSkip
	}
	if lastRun <= 0 {
		return DecisionSeed
	}

	scheduled := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if now.Before(scheduled) {
		return DecisionSkip
	}
	if lastRun >= scheduled.UnixMilli() {
		return DecisionSkip
	}
	return DecisionRun
}

// ParseDailyTime reads an "HH:MM" setting. An empty or malformed value means
// the daily purge is off, which is also what an operator clearing the field
// intends.
func ParseDailyTime(value string) (hour, minute int, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, false
	}
	left, right, found := strings.Cut(value, ":")
	if !found {
		return 0, 0, false
	}
	hour, err := strconv.Atoi(strings.TrimSpace(left))
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, false
	}
	minute, err = strconv.Atoi(strings.TrimSpace(right))
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

// Sweep applies the policy once. lastRun is when the daily purge last ran;
// the returned result says whether it ran this time, so the caller can store
// the new value.
func (s *Store) Sweep(ctx context.Context, policy Retention, lastRun int64) (SweepResult, error) {
	now := time.Now()
	var result SweepResult

	if policy.OrphanTTL > 0 {
		removed, err := s.DeleteOrphans(ctx, policy.OrphanTTL)
		if err != nil {
			return result, err
		}
		result.Orphans = removed
	}

	if policy.AfterDays > 0 {
		cutoff := now.AddDate(0, 0, -policy.AfterDays).UnixMilli()
		dropped, err := s.DiscardBefore(ctx, cutoff)
		if err != nil {
			return result, err
		}
		result.Aged = dropped
	}

	if DecidePurge(policy.DailyAt, now, lastRun) == DecisionRun {
		// Everything, whatever its age — which is what "empty at 03:00"
		// means. An upload still sitting in someone's composer is untouched:
		// it has not been sent, so the orphan window is what governs it, and
		// wiping it would break a message being written.
		dropped, err := s.DiscardBefore(ctx, now.UnixMilli())
		if err != nil {
			return result, err
		}
		result.Purged = dropped
		result.RanDaily = true
	}
	return result, nil
}

// DiscardBefore drops the bytes of every attachment written before a moment,
// keeping the rows. Passing the current time means "all of them".
//
// Only attachments that belong to a message: an upload still waiting for one
// has nothing recorded about it worth keeping, so it is the orphan sweep's to
// delete outright rather than this one's to hollow out.
func (s *Store) DiscardBefore(ctx context.Context, createdBefore int64) (int64, error) {
	result, err := s.db.Exec(ctx,
		`UPDATE attachments SET data = ?, discarded_at = ?
		 WHERE discarded_at = 0 AND message_id IS NOT NULL AND created_at < ?`,
		[]byte{}, time.Now().UnixMilli(), createdBefore)
	if err != nil {
		return 0, fmt.Errorf("conversation: discard before %d: %w", createdBefore, err)
	}
	dropped, _ := result.RowsAffected()
	return dropped, nil
}

// Held reports what the instance is currently storing, so an operator can see
// whether their policy is doing anything rather than trusting that it is.
func (s *Store) Held(ctx context.Context) (count int64, bytes int64, err error) {
	err = s.db.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM attachments WHERE discarded_at = 0`).
		Scan(&count, &bytes)
	if err != nil {
		return 0, 0, fmt.Errorf("conversation: held attachments: %w", err)
	}
	return count, bytes, nil
}

// UserStorage is one account's share of what the instance is holding.
type UserStorage struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Count  int64  `json:"count"`
	Bytes  int64  `json:"bytes"`
}

// HeldByUser is Held, broken down by whose files they are.
//
// Ranked and capped in SQL for the reason usage.GroupBy gives: taking the top
// twenty by size and then re-sorting them by count would be the top twenty of
// the wrong thing. The username is joined here rather than resolved by the
// caller because a column of ULIDs answers "who is filling the disk" only in
// principle.
func (s *Store) HeldByUser(ctx context.Context, limit int) ([]UserStorage, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT a.user_id, MAX(COALESCE(u.username, a.user_id)), COUNT(*), COALESCE(SUM(a.size), 0)
		FROM attachments a
		LEFT JOIN users u ON u.id = a.user_id
		WHERE a.discarded_at = 0
		GROUP BY a.user_id
		ORDER BY COALESCE(SUM(a.size), 0) DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("conversation: held by user: %w", err)
	}
	defer func() { _ = rows.Close() }()

	held := make([]UserStorage, 0, limit)
	for rows.Next() {
		var entry UserStorage
		if err := rows.Scan(&entry.UserID, &entry.Name, &entry.Count, &entry.Bytes); err != nil {
			return nil, fmt.Errorf("conversation: held by user: %w", err)
		}
		held = append(held, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conversation: held by user: %w", err)
	}
	return held, nil
}

// Discarded counts the rows whose bytes retention has already dropped. They
// still cost a row and still name a message; what they no longer cost is the
// storage, which is the number beside them on the page.
func (s *Store) Discarded(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM attachments WHERE discarded_at <> 0`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("conversation: discarded attachments: %w", err)
	}
	return count, nil
}
