package conversation

import (
	"context"
	"fmt"
	"sort"
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
	// A lastRun later than now cannot be a run of this schedule. It was recorded
	// while the clock was ahead, or the clock has been corrected since, and
	// counting it as done skipped every later day's purge until the real clock
	// passed it.
	if lastRun >= scheduled.UnixMilli() && lastRun <= now.UnixMilli() {
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
// Two kinds are covered: an attachment on a message, and an Image Lab picture
// in the library. The second has no message, but the cleanup policy applies to
// it as the Image Lab documentation says it does, so it is selected by its
// generation record. An upload still waiting for a message is neither: it has
// nothing recorded about it worth keeping, so it is the orphan sweep's to
// delete outright rather than this one's to hollow out.
func (s *Store) DiscardBefore(ctx context.Context, createdBefore int64) (int64, error) {
	result, err := s.db.Exec(ctx,
		`UPDATE attachments SET data = ?, discarded_at = ?
		 WHERE discarded_at = 0 AND created_at < ?
		   AND (message_id IS NOT NULL OR id IN (SELECT attachment_id FROM image_generations))`,
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

// StorageTotals is everything the storage page shows, read together.
type StorageTotals struct {
	HeldCount int64
	HeldBytes int64
	Discarded int64
	ByUser    []UserStorage
}

// Storage answers the storage page in one scan: what is held, what has been
// discarded, and the held total broken down by account. The page asks every few
// seconds, and three separate reads each walked the whole attachments table:
// discarded_at has no index that would narrow them.
func (s *Store) Storage(ctx context.Context) (StorageTotals, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.user_id, MAX(COALESCE(u.username, a.user_id)),
			COALESCE(SUM(CASE WHEN a.discarded_at = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN a.discarded_at = 0 THEN a.size ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN a.discarded_at <> 0 THEN 1 ELSE 0 END), 0)
		FROM attachments a
		LEFT JOIN users u ON u.id = a.user_id
		GROUP BY a.user_id`)
	if err != nil {
		return StorageTotals{}, fmt.Errorf("conversation: storage: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := StorageTotals{ByUser: []UserStorage{}}
	for rows.Next() {
		var entry UserStorage
		var discarded int64
		if err := rows.Scan(&entry.UserID, &entry.Name, &entry.Count, &entry.Bytes, &discarded); err != nil {
			return StorageTotals{}, fmt.Errorf("conversation: storage scan: %w", err)
		}
		out.Discarded += discarded
		// An account whose files are all discarded holds nothing, so it is left
		// out of the breakdown rather than listed at zero.
		if entry.Count == 0 {
			continue
		}
		out.HeldCount += entry.Count
		out.HeldBytes += entry.Bytes
		out.ByUser = append(out.ByUser, entry)
	}
	if err := rows.Err(); err != nil {
		return StorageTotals{}, fmt.Errorf("conversation: storage: %w", err)
	}
	// Heaviest first, then by id, so accounts with equal holdings keep a stable
	// place on the page.
	sort.Slice(out.ByUser, func(i, j int) bool {
		if out.ByUser[i].Bytes != out.ByUser[j].Bytes {
			return out.ByUser[i].Bytes > out.ByUser[j].Bytes
		}
		return out.ByUser[i].UserID < out.ByUser[j].UserID
	})
	return out, nil
}
