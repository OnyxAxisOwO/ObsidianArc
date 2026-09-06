package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func capabilityStore(t *testing.T) *group.Store {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "groups.db"),
		MaxOpenConns: 4,
		MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return group.NewStore(db)
}

// Whether a group may delete its conversations is decided on the server. The
// client hides the button, but hiding a button is a courtesy and this is the
// rule.
func TestDeleteAllowedFollowsTheGroup(t *testing.T) {
	ctx := context.Background()
	groups := capabilityStore(t)

	held, err := groups.Create(ctx, nil, group.CreateInput{Name: "On the record"})
	if err != nil {
		t.Fatal(err)
	}
	free, err := groups.Create(ctx, nil, group.CreateInput{
		Name: "Ordinary", AllowStats: true, AllowDeleteConversations: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// CreateInput takes the capability as written, so a group made with the
	// zero value has it off — which is what the admin handler defaults to
	// true before calling, and what this half of the test relies on.
	if held.AllowDeleteConversations {
		t.Fatal("the fixture group was created able to delete")
	}

	member := user.User{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", GroupID: held.ID, Role: user.RoleUser}
	if err := deleteAllowed(ctx, groups, member); err == nil {
		t.Error("a member of a group without the capability was allowed to delete")
	}

	// An administrator always may: the capability exists to hold an
	// instance's members to a record, not to lock its operator out of one.
	operator := user.User{ID: "01BRZ3NDEKTSV4RRFFQ69G5FBW", GroupID: held.ID, Role: user.RoleAdmin}
	if err := deleteAllowed(ctx, groups, operator); err != nil {
		t.Errorf("an administrator was refused: %v", err)
	}

	allowed := user.User{ID: "01CRZ3NDEKTSV4RRFFQ69G5FCX", GroupID: free.ID, Role: user.RoleUser}
	if err := deleteAllowed(ctx, groups, allowed); err != nil {
		t.Errorf("a member of a group with the capability was refused: %v", err)
	}

	// A group that cannot be read grants it, matching what the account
	// payload tells the client: a failed lookup must not quietly take away
	// something somebody has.
	orphan := user.User{ID: "01DRZ3NDEKTSV4RRFFQ69G5FDY", GroupID: "01NOSUCHGROUPHERE00000", Role: user.RoleUser}
	if err := deleteAllowed(ctx, groups, orphan); err != nil {
		t.Errorf("an unreadable group refused instead of granting: %v", err)
	}
}

// Both capabilities reach the database and come back, including through an
// edit that never mentions them.
func TestGroupCapabilitiesRoundTrip(t *testing.T) {
	ctx := context.Background()
	groups := capabilityStore(t)

	created, err := groups.Create(ctx, nil, group.CreateInput{
		Name: "Members", AllowStats: true, AllowDeleteConversations: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	reloaded, err := groups.ByID(ctx, nil, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.AllowStats || reloaded.AllowDeleteConversations {
		t.Fatalf("stored %+v, want stats on and delete off", reloaded)
	}

	renamed := "Members, renamed"
	updated, err := groups.Update(ctx, nil, created.ID, group.Update{Name: &renamed})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.AllowStats || updated.AllowDeleteConversations {
		t.Errorf("an unrelated edit moved the capabilities: %+v", updated)
	}

	on := true
	back, err := groups.Update(ctx, nil, created.ID, group.Update{AllowDeleteConversations: &on})
	if err != nil {
		t.Fatal(err)
	}
	if !back.AllowDeleteConversations {
		t.Error("granting the capability back did not take")
	}
}

// A user's own usage history is their own. The endpoint takes no parameter
// that says whose rows these are — the account comes from the session — and
// this is what stops somebody adding one later.
func TestUsageHistoryIsScopedToTheCaller(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	visitor := in.register("visitor", "another-password")

	// Whatever a caller sends, the answer is about them. The ids below are
	// the other account's, in every shape the handler could plausibly read.
	for _, query := range []string{
		"",
		"?user_id=" + founder.userID,
		"?userID=" + founder.userID,
		"?id=" + founder.userID,
		"?limit=10&user_id=" + founder.userID,
	} {
		response := in.do(http.MethodGet, "/api/usage/me/history"+query, nil, visitor)
		if response.Code != http.StatusOK {
			t.Fatalf("%q: status = %d: %s", query, response.Code, response.Body.String())
		}
		if body := response.Body.String(); strings.Contains(body, founder.userID) {
			t.Errorf("%q: the other account's id came back: %s", query, body)
		}
	}

	// And signing out is the end of it.
	if response := in.do(http.MethodGet, "/api/usage/me/history", nil, nil); response.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: status = %d, want 401", response.Code)
	}
}
