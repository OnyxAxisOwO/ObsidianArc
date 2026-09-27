package user

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

func TestRecentSignupsReturnsBoundedStoredDecisions(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "recent.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	old, err := store.Create(ctx, nil, CreateInput{
		Username: "olduser", Email: "old@example.com", PasswordHash: "opaque",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE users SET created_at = ? WHERE id = ?`,
		time.Now().Add(-48*time.Hour).UnixMilli(), old.ID); err != nil {
		t.Fatal(err)
	}
	newer, err := store.Create(ctx, nil, CreateInput{
		Username: "user37854", Email: "u491421@uberip.com", PasswordHash: "opaque",
		Status: StatusDisabled, APIRestricted: true,
		APIRestrictedUntil:   time.Now().Add(time.Hour).UnixMilli(),
		APIRestrictionSource: "signup_review",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, nil, CreateInput{
		Username: "othernew", Email: "other@example.com", PasswordHash: "opaque",
	}); err != nil {
		t.Fatal(err)
	}
	items, err := store.RecentSignups(ctx, nil, "new@uberip.com", time.Now().Add(-24*time.Hour).UnixMilli(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Username != newer.Username ||
		items[0].Status != StatusDisabled || !items[0].APIRestricted ||
		items[0].APIRestrictionSource != "signup_review" || items[0].APIRestrictedUntil == 0 {
		t.Fatalf("recent signups = %+v", items)
	}
}
