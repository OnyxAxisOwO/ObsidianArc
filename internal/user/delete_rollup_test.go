package user

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// The hourly rollup has no foreign key to users, so deleting an account must
// remove its rollup rows itself. Otherwise windowed totals keep counting spend
// whose ledger rows the cascade already removed.
func TestDeletingAnAccountRemovesItsRollupRows(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "delete-rollup.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"gone", "kept"} {
		if _, err := db.Exec(ctx,
			`INSERT INTO users (id, username, username_lower, password_hash, role, status, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, id, id, "x", RoleUser, StatusActive, 1, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx,
			`INSERT INTO usage_rollup_hourly (hour_ts, user_id, requests) VALUES (?, ?, ?)`,
			time.Now().Add(-2*time.Hour).UnixMilli(), id, 3); err != nil {
			t.Fatal(err)
		}
	}

	if err := NewStore(db).Delete(ctx, nil, "gone"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	count := func(id string) int {
		var n int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM usage_rollup_hourly WHERE user_id = ?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := count("gone"); got != 0 {
		t.Fatalf("deleted account keeps %d rollup rows", got)
	}
	if got := count("kept"); got != 1 {
		t.Fatalf("another account's rollup rows = %d, want 1", got)
	}
}
