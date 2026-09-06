package settings

import (
	"context"
	"fmt"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// LockKey is the row every instance-wide check-then-write serialises on.
//
// Any one known key would do; this one is chosen because Defaults always
// supplies it, so the upsert below has something to write, and because
// nothing reads its updated_at for meaning. Everything takes the same row on
// purpose: the invariants that need this are all about the same population of
// accounts, so a registration deciding whether it is the first must not run
// beside a demotion deciding whether it is the last.
const LockKey = RegistrationEnabled

// Lock takes the row lock that a check followed by a write has to hold when
// what it decides is instance-wide.
//
// "Is there an administrator left", "is this account the first one", "has this
// owner used up its allowance" are each a read and then a write, and two of
// them running at once each see the world the other is about to change, so
// both proceed. A mutex would cover one process; the deployment notes allow a
// second instance against one database, so the lock has to live there.
//
// Upserting a known key without changing its value is what takes it on both
// supported engines. The value is written back as itself, so the cached copy
// of the settings that Service holds stays correct — this only moves a lock,
// never a setting.
//
// It must be called inside a transaction. On its own it locks the row and then
// immediately lets it go, which is the same as not locking at all.
func Lock(ctx context.Context, tx *database.Tx) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET updated_at = settings.updated_at`,
		LockKey, Defaults[LockKey], time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("settings: take the instance lock: %w", err)
	}
	return nil
}
