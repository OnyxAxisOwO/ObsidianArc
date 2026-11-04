package pow

import (
	"context"
	"fmt"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// pruneGraceSeconds keeps a spent salt this long past its challenge's expiry.
// Each instance reads its own clock for both the expiry check and the prune,
// so one whose clock runs behind would still accept a challenge that another
// instance had already pruned, and accept it a second time. The margin is far
// beyond the drift NTP allows between clocks.
const pruneGraceSeconds = 60

// SaltStore spends salts in the database. Two instances that share a database
// then agree on which challenges have been used, which a process's own memory
// cannot guarantee.
type SaltStore struct {
	db *database.DB
}

func NewSaltStore(db *database.DB) *SaltStore {
	return &SaltStore{db: db}
}

// Spend records salt as used until expires and reports whether this call was
// the first to do so.
//
// The insert is the check: the salt is the primary key, so a second arrival
// conflicts and changes nothing, on both engines. A read followed by a write
// would let two requests racing with one solution both see it unspent.
func (s *SaltStore) Spend(ctx context.Context, salt string, expires int64) (bool, error) {
	// Expired rows go first, once the grace has passed. Their challenges are
	// refused by the expiry check before a spend is asked for, so dropping
	// them cannot change an answer on an instance whose clock agrees.
	if _, err := s.db.Exec(ctx, `DELETE FROM pow_salts WHERE expires_at < ?`,
		time.Now().Unix()-pruneGraceSeconds); err != nil {
		return false, fmt.Errorf("pow: prune salts: %w", err)
	}
	result, err := s.db.Exec(ctx,
		`INSERT INTO pow_salts (salt, expires_at) VALUES (?, ?)
		 ON CONFLICT (salt) DO NOTHING`,
		salt, expires)
	if err != nil {
		return false, fmt.Errorf("pow: spend salt: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("pow: spend salt: %w", err)
	}
	return affected == 1, nil
}
