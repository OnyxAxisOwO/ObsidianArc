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

// A group that cannot be read refuses the web terminal. The terminal is a way
// in rather than something the account owns, so an unreadable grant is not a
// reason to open it.
func TestTerminalAllowedRefusesWhenTheGroupCannotBeRead(t *testing.T) {
	db, groups := openGroupsForTest(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	err := terminalAllowed(context.Background(), groups, user.User{ID: "u1", GroupID: "any-group"})
	var refused *httpx.Error
	if !errors.As(err, &refused) || refused.Code != "terminal_not_permitted" {
		t.Fatalf("a failed group read answered %v, want terminal_not_permitted", err)
	}
}

// Unlike terminalAllowed, consoleAdmits keeps a failed read as an error. An
// open SSH session asks again before each command, and a database outage must
// not end it with the message for a revoked grant.
func TestConsoleAdmitsKeepsAFailedGroupReadAsAnError(t *testing.T) {
	db, groups := openGroupsForTest(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// The group lookup is the first thing consoleAdmits does for an account
	// that is not an administrator, so the services it would ask afterwards
	// are never reached and nil is safe for them here.
	allowed, err := consoleAdmits(context.Background(), groups, nil, nil, user.User{ID: "u1", GroupID: "any-group"})
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
}

// A group that does not exist is a plain refusal, not a fault: the account's
// grant is gone, and the session should end with the revoked message.
func TestConsoleAdmitsRefusesAGroupThatDoesNotExist(t *testing.T) {
	_, groups := openGroupsForTest(t)
	allowed, err := consoleAdmits(context.Background(), groups, nil, nil, user.User{ID: "u1", GroupID: "no-such-group"})
	if err != nil || allowed {
		t.Fatalf("a missing group answered allowed=%v err=%v, want a plain refusal", allowed, err)
	}
}
