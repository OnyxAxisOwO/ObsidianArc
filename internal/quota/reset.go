package quota

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// clearAllowance is what a reset card does inside the reservation that spent it:
// it deletes the current bucket of each allowance window the card names.
//
// Only the current bucket goes. A limit check reads only that bucket, and so does
// the usage screen, so older buckets are not part of what a reset restores.
// Deleting them as well would take more than one row of a window kind in this
// transaction, which the lock order forbids, and an older bucket is exactly what
// a release of an earlier reservation can hold. The minute is not an allowance
// window: a card never clears it, and the charge this request put on it stays as
// reserveRate left it.
//
// Every allowance window's current bucket is locked before the first delete, in
// AllowanceWindows order. The refused walk had locked only the windows up to the
// one that refused it. A card that names the month but not the week would delete
// the month's row before the retry took the week's, while a settlement in the next
// five-hour bucket takes the week and then the month, so each would hold the row
// the other wanted. Touching every window first means each delete finds its row
// already held, in the order every other path takes them.
func clearAllowance(ctx context.Context, tx database.Queryer, key string, anchor int64, now time.Time, windows []string) error {
	for _, window := range AllowanceWindows {
		// A zero delta. It takes the row lock whether or not the bucket has a row
		// yet, and the retry that follows charges every window anyway.
		if _, err := bump(ctx, tx, key, window, bucketStart(window, now, anchor), 0, 0, 0); err != nil {
			return err
		}
	}
	for _, window := range AllowanceWindows {
		if !resetsWindow(windows, window) {
			continue
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM usage_counters WHERE scope_key = ? AND window_kind = ? AND window_start = ?`,
			key, window, bucketStart(window, now, anchor)); err != nil {
			return err
		}
	}
	return nil
}

// deleteCounters deletes the counter rows that a reset names, under the global
// reset row. A single DELETE takes its rows in whatever order the database scans
// them, which can put a later kind's row ahead of an earlier one's and so
// reverse the lock order. One statement per kind, in counterKinds order, does
// not. No two resets can meet, nor a reset and a prune, because all of them hold
// the global reset row first.
// scope, when not empty, narrows the rows, with args for its placeholders.
func deleteCounters(ctx context.Context, tx database.Queryer, names []string, scope string, args ...any) error {
	for _, window := range counterKinds {
		if !resetsWindow(names, window) {
			continue
		}
		query := `DELETE FROM usage_counters WHERE window_kind = ?`
		kindArgs := []any{window}
		if scope != "" {
			query += ` AND ` + scope
			kindArgs = append(kindArgs, args...)
		}
		if _, err := tx.Exec(ctx, query, kindArgs...); err != nil {
			return err
		}
	}
	return nil
}

// The global reset is an allowance boundary rather than a second kind of
// counter. Keeping one timestamp means a reset reaches every existing account
// without rewriting their registration dates; an account created afterwards
// still starts at its own later creation time.
const globalResetKey = "quota.global_reset_at"

func allowanceAnchor(ctx context.Context, q database.Queryer, createdAt int64) (int64, error) {
	resetAt, err := globalResetAt(ctx, q)
	if err != nil {
		return 0, err
	}
	return anchorFor(createdAt, resetAt), nil
}

// globalResetAt is zero when no reset has ever been made.
func globalResetAt(ctx context.Context, q database.Queryer) (int64, error) {
	var raw string
	err := q.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, globalResetKey).Scan(&raw)
	if database.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("quota: read global reset: %w", err)
	}
	resetAt, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		// Read as no reset, which drops the usage counted against the real one:
		// every counter is looked up from the account's creation. Not refused,
		// because a refused reservation would stop every account on the instance,
		// but logged, because only a reset writes this row.
		slog.Warn("quota: global reset is not a timestamp, reading it as never reset", "value", raw, "error", err)
		return 0, nil
	}
	return resetAt, nil
}

func anchorFor(createdAt, resetAt int64) int64 {
	if resetAt <= createdAt {
		return createdAt
	}
	return resetAt
}

// lockAllowanceBoundary takes the global reset row, the first lock in the lock
// order above. A reservation that charges the allowance windows, every
// administrator reset and every prune take it. This no-op upsert takes the same
// database row lock ResetAll writes, including when two server processes share the
// database.
func lockAllowanceBoundary(ctx context.Context, tx database.Queryer) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET updated_at = settings.updated_at`,
		globalResetKey, "0", time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("quota: lock global reset: %w", err)
	}
	return nil
}

// LockAllowanceBoundary is lockAllowanceBoundary for a caller outside this
// package. The card package spends a card inside a transaction, and must take
// this row before the card row, as every reservation that may spend a card does.
func LockAllowanceBoundary(ctx context.Context, tx database.Queryer) error {
	return lockAllowanceBoundary(ctx, tx)
}

func lockedAllowanceAnchor(ctx context.Context, tx database.Queryer, createdAt int64) (int64, error) {
	// A reset and a reservation must agree which side of the boundary the
	// allowance check belongs to, so the reservation reads the anchor under the
	// same lock the reset writes it under.
	if err := lockAllowanceBoundary(ctx, tx); err != nil {
		return 0, err
	}
	return allowanceAnchor(ctx, tx, createdAt)
}

// ResetAll puts every account's allowance back to its full amount and starts
// each allowance window again from now.
//
// The counters are deleted rather than zeroed. An absent row and a row at
// zero read the same to everything above this — bump() upserts, SummaryFor
// treats a missing row as nothing spent — and deleting leaves nothing behind
// to go stale when the window it belonged to rolls over.
//
// The ledger is deliberately untouched. What was spent is still what was
// spent; this is a decision about what may be spent next, and rewriting the
// record to agree with it would destroy the only account of either.
func (s *Service) ResetAll(ctx context.Context) error {
	now := time.Now().UnixMilli()
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// Reservations hold this row lock so a limit check cannot be written
		// against an obsolete allowance after the counters have been cleared.
		// Settlement reads the epoch before its write transaction, allowing
		// independent completed turns to update their own counters concurrently.
		if _, err := tx.Exec(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			globalResetKey, strconv.FormatInt(now, 10), now); err != nil {
			return err
		}
		return deleteCounters(ctx, tx, nil, "")
	})
	if err != nil {
		return fmt.Errorf("quota: reset all: %w", err)
	}
	return nil
}

// ResetGroup does the same for everyone in a group.
//
// Named by the group rather than by its members, because enumerating them
// first needs a page size — and a page size on this path is a silent cap on
// how much of a group a reset actually reaches. It was one: the caller asked
// the user list for exactly as many rows as the group has, and the list
// clamps anything above two hundred back down to fifty, so resetting a large
// group quietly reset fifty accounts and reported that it had.
//
// The scope key is built in SQL with the same "u:" prefix scopeKey writes,
// concatenated with || because that is the one string operator both engines
// spell the same way.
func (s *Service) ResetGroup(ctx context.Context, groupID string) error {
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockAllowanceBoundary(ctx, tx); err != nil {
			return err
		}
		return deleteCounters(ctx, tx, nil,
			`scope_key IN (SELECT ? || id FROM users WHERE group_id = ?)`, userScopePrefix, groupID)
	})
	if err != nil {
		return fmt.Errorf("quota: reset group: %w", err)
	}
	return nil
}

// ResetWindows clears usage counters for specific windows on named accounts.
// An empty window list or one containing "full" clears every window.
//
// q is nil to clear in one transaction per batch, or the caller's transaction
// when the clear has to stand or fall with something else. A card's reset is
// the case: the card is marked spent in that transaction, and a clear that
// committed on its own could restore an allowance for a card whose spend then
// failed, or leave a marked card with its allowance unrestored.
func (s *Service) ResetWindows(ctx context.Context, q database.Queryer, userIDs []string, windows []string) error {
	const batch = 200
	for start := 0; start < len(userIDs); start += batch {
		end := min(start+batch, len(userIDs))
		chunk := userIDs[start:end]

		args := make([]any, len(chunk))
		for i, userID := range chunk {
			args[i] = scopeKey(userID)
		}
		scope := `scope_key IN (` + database.Placeholders(len(chunk)) + `)`

		reset := func(tx database.Queryer) error {
			if err := lockAllowanceBoundary(ctx, tx); err != nil {
				return err
			}
			return deleteCounters(ctx, tx, windows, scope, args...)
		}
		var err error
		if q == nil {
			err = s.db.Tx(ctx, func(tx *database.Tx) error { return reset(tx) })
		} else {
			err = reset(q)
		}
		if err != nil {
			return fmt.Errorf("quota: reset windows: %w", err)
		}
	}
	return nil
}

// ResetCardWindows is ResetWindows for a card that has been spent. A card puts
// the allowance back and never the per-minute counters: those protect the
// upstream provider from bursts, so no card may lift them. An empty list or one
// that names "full" means every allowance window, and a minute window named in
// the list is ignored.
func (s *Service) ResetCardWindows(ctx context.Context, q database.Queryer, userIDs []string, windows []string) error {
	var allowance []string
	for _, window := range AllowanceWindows {
		if resetsWindow(windows, window) {
			allowance = append(allowance, string(window))
		}
	}
	if len(allowance) == 0 {
		return nil
	}
	return s.ResetWindows(ctx, q, userIDs, allowance)
}

// Reset does the same for named accounts across all windows.
//
// In batches, because the caller may hand it every member of a group and one
// statement with ten thousand placeholders is a statement no database wants.
func (s *Service) Reset(ctx context.Context, userIDs []string) error {
	return s.ResetWindows(ctx, nil, userIDs, nil)
}
