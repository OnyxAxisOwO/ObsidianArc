package user

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

// A rehash replaces the hash it verified and nothing else. The condition is the
// whole guarantee: a password changed after the check is already stored, and a
// replace that ignored what is stored would write the old hash back over it. The
// condition is checked on both engines, because each one evaluates it under its
// own locking.
func TestReplacePasswordHashOnlyReplacesTheVerifiedHash(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			cfg := config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "replace.db"), MaxOpenConns: 4}
			if driver == "postgres" {
				cfg = dbtest.Postgres(t, "a conditional replace has to hold on both database engines")
			}
			ctx := context.Background()
			db, err := database.Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if _, err := db.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			groups := group.NewStore(db)
			if _, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true}); err != nil {
				t.Fatal(err)
			}
			users := NewStore(db)
			account, err := users.Create(ctx, nil, CreateInput{Username: "member", PasswordHash: "old-hash"})
			if err != nil {
				t.Fatal(err)
			}

			replaced, err := users.ReplacePasswordHash(ctx, nil, account.ID, "stale-hash", "new-hash")
			if err != nil {
				t.Fatal(err)
			}
			if replaced {
				t.Fatal("a hash that is no longer stored was replaced")
			}
			stored, err := users.PasswordHash(ctx, nil, account.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored != "old-hash" {
				t.Fatalf("the stored hash after a refused replace = %q, want it untouched", stored)
			}

			replaced, err = users.ReplacePasswordHash(ctx, nil, account.ID, "old-hash", "new-hash")
			if err != nil {
				t.Fatal(err)
			}
			if !replaced {
				t.Fatal("the hash that was verified was not replaced")
			}
			stored, err = users.PasswordHash(ctx, nil, account.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored != "new-hash" {
				t.Fatalf("the stored hash after a replace = %q, want new-hash", stored)
			}
		})
	}
}
