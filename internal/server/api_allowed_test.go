package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A group that does not exist withholds API access. A group that could not be
// read says nothing about the grant, so the failure surfaces as a fault rather
// than as a refusal that sends the person to ask their administrator again.
func TestAPIAllowedTreatsAFailedGroupReadAsAFault(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "api.db"),
		MaxOpenConns: 4, MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	// A failing assertion returns before the explicit close below, and on
	// Windows the temp directory cannot be removed while the file is open.
	// Closing twice is harmless: the second close is a no-op.
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	set := settings.New(db)
	if err := set.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if err := set.Set(ctx, settings.APIEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	groups := group.NewStore(db)

	orphan := user.User{ID: "u1", GroupID: "no-such-group"}
	err = apiAllowed(ctx, set, groups, orphan)
	var refused *httpx.Error
	if !errors.As(err, &refused) || refused.Code != "api_not_permitted" {
		t.Fatalf("a missing group answered %v, want api_not_permitted", err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	err = apiAllowed(ctx, set, groups, user.User{ID: "u1", GroupID: "any-group"})
	if err == nil {
		t.Fatal("a group that could not be read was reported as allowed")
	}
	if errors.As(err, &refused) {
		t.Errorf("a failed read was answered as a refusal: %v", err)
	}
}
