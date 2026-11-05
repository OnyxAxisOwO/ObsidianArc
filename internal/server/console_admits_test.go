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
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// openGroupsForTest returns a group store over a migrated scratch database.
// The database is returned too, so a test can close it to make every read fail.
func openGroupsForTest(t *testing.T) (*database.DB, *group.Store) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "console.db"),
		MaxOpenConns: 4, MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	// Closing twice is harmless, and a failing assertion returns before the
	// explicit close the tests make.
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db, group.NewStore(db)
}

// Unlike terminalAllowed, consoleAdmits keeps a failed read as an error. An
// open SSH session asks again before each command, and a database outage must
// not end it with the message for a revoked grant.
func TestConsoleAdmitsKeepsAFailedGroupReadAsAnError(t *testing.T) {
	db, groups := openGroupsForTest(t)
	ctx := t.Context()
	account := user.User{ID: "u1", GroupID: "no-such-group"}
	if allowed, err := consoleAdmits(ctx, groups, nil, nil, account); err != nil || allowed {
		t.Fatalf("missing group: allowed=%v, err=%v, want a plain refusal", allowed, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// The group lookup is the first thing consoleAdmits does for an account
	// that is not an administrator, so the services it would ask afterwards
	// are never reached and nil is safe for them here.
	allowed, err := consoleAdmits(ctx, groups, nil, nil, account)
	if err == nil {
		t.Fatalf("a failed group read was answered allowed=%v with no error", allowed)
	}
	if allowed {
		t.Fatal("a failed group read let the console open")
	}
	var refused *httpx.Error
	if errors.As(err, &refused) {
		t.Errorf("a failed read was answered as a refusal: %v", err)
	}
	if err := terminalAllowed(ctx, groups, account); !errors.As(err, &refused) || refused.Code != "terminal_not_permitted" {
		t.Fatalf("failed group read over HTTP: %v, want terminal_not_permitted", err)
	}
}
