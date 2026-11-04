package pow

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// Instances read their own clocks, so one that runs a little behind still
// accepts a challenge that has just expired on another. A spent salt must
// therefore outlive its expiry by the grace, or that instance would accept it a
// second time once the row had been pruned.
func TestASpentSaltOutlivesItsExpiryByTheClockGrace(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "grace.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store := NewSaltStore(db)

	// Expired thirty seconds ago: inside the grace, outside the challenge.
	expired := time.Now().Unix() - 30
	if fresh, err := store.Spend(ctx, "skewed", expired); err != nil || !fresh {
		t.Fatalf("first spend = %v, %v, want fresh", fresh, err)
	}

	// Any later spend prunes. The row must survive it.
	if _, err := store.Spend(ctx, "later", time.Now().Unix()+300); err != nil {
		t.Fatal(err)
	}
	fresh, err := store.Spend(ctx, "skewed", expired)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("a salt thirty seconds past its expiry was pruned, so a skewed instance could accept it again")
	}

	// And a row past the grace is pruned, so the table does not grow forever.
	stale := time.Now().Unix() - pruneGraceSeconds - 60
	if fresh, err := store.Spend(ctx, "stale", stale); err != nil || !fresh {
		t.Fatalf("stale spend = %v, %v, want fresh", fresh, err)
	}
	if _, err := store.Spend(ctx, "later-again", time.Now().Unix()+300); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM pow_salts WHERE salt = ?`, "stale").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("a salt past the grace was kept")
	}
}
