package apikey

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func newStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "keys.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	groups := group.NewStore(db)
	membership, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	users := user.NewStore(db)
	owner, err := users.Create(ctx, nil, user.CreateInput{
		Username: "owner", PasswordHash: "x", GroupID: membership.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := users.Create(ctx, nil, user.CreateInput{
		Username: "stranger", PasswordHash: "x", GroupID: membership.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(db), owner.ID, stranger.ID
}

func TestIssuedTokenResolvesBackToItsKey(t *testing.T) {
	ctx := context.Background()
	store, owner, _ := newStore(t)

	created, token, err := store.Issue(ctx, owner, "laptop", 0)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !strings.HasPrefix(token, tokenPrefix) {
		t.Errorf("token %q does not carry the instance prefix", token)
	}

	resolved, err := store.Resolve(ctx, token)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.ID != created.ID || resolved.UserID != owner {
		t.Errorf("resolved %+v, want the key just issued for %s", resolved, owner)
	}
}

// The property the whole design rests on: the value is not recoverable from
// anything the server kept.
func TestTokenIsNotStoredAnywhere(t *testing.T) {
	ctx := context.Background()
	store, owner, _ := newStore(t)

	_, token, err := store.Issue(ctx, owner, "laptop", 0)
	if err != nil {
		t.Fatal(err)
	}

	var found int
	err = store.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM api_keys WHERE token_hash = ? OR prefix = ? OR name = ?`,
		token, token, token).Scan(&found)
	if err != nil {
		t.Fatal(err)
	}
	if found != 0 {
		t.Error("the token itself was written to a column")
	}

	listed, err := store.List(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed %d keys, want 1", len(listed))
	}
	if strings.Contains(token, listed[0].Prefix) && len(listed[0].Prefix) >= len(token) {
		t.Error("the stored prefix is the whole token")
	}
}

func TestUnknownTokenDoesNotResolve(t *testing.T) {
	ctx := context.Background()
	store, owner, _ := newStore(t)
	if _, _, err := store.Issue(ctx, owner, "laptop", 0); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"", "   ", "sk-oa-nonsense", "not-a-key"} {
		if _, err := store.Resolve(ctx, bad); err == nil {
			t.Errorf("resolve(%q) succeeded", bad)
		}
	}
}

func TestExpiredKeyIsRefused(t *testing.T) {
	ctx := context.Background()
	store, owner, _ := newStore(t)

	// Issued live, then expired: Issue refuses a past expiry outright, and
	// what matters here is a key that aged out while it existed.
	created, token, err := store.Issue(ctx, owner, "temporary", time.Now().Add(time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(ctx, token); err != nil {
		t.Fatalf("a live key was refused: %v", err)
	}

	past := time.Now().Add(-time.Minute).UnixMilli()
	if _, err := store.Update(ctx, owner, created.ID, Update{ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(ctx, token); err == nil {
		t.Fatal("an expired key still resolved")
	}
}

func TestIssueRefusesAnExpiryAlreadyPast(t *testing.T) {
	ctx := context.Background()
	store, owner, _ := newStore(t)

	past := time.Now().Add(-time.Hour).UnixMilli()
	if _, _, err := store.Issue(ctx, owner, "doomed", past); err == nil {
		t.Fatal("a key that was already expired was issued")
	}
}

func TestNameIsRequiredAndBounded(t *testing.T) {
	ctx := context.Background()
	store, owner, _ := newStore(t)

	for _, bad := range []string{"", "   ", strings.Repeat("x", MaxNameChars+1)} {
		if _, _, err := store.Issue(ctx, owner, bad, 0); err == nil {
			t.Errorf("issue with name %q succeeded", bad)
		}
	}
}

// Another account's key id must be useless, not merely hard to find: the
// owner is part of the WHERE clause rather than a check beside it.
func TestOneAccountCannotTouchAnothersKey(t *testing.T) {
	ctx := context.Background()
	store, owner, stranger := newStore(t)

	created, token, err := store.Issue(ctx, owner, "laptop", 0)
	if err != nil {
		t.Fatal(err)
	}

	renamed := "stolen"
	if _, err := store.Update(ctx, stranger, created.ID, Update{Name: &renamed}); err == nil {
		t.Error("a stranger renamed someone else's key")
	}
	if err := store.Delete(ctx, stranger, created.ID); err == nil {
		t.Error("a stranger deleted someone else's key")
	}

	// Still working, and still called what its owner called it.
	resolved, err := store.Resolve(ctx, token)
	if err != nil {
		t.Fatalf("the key stopped working: %v", err)
	}
	if resolved.Name != "laptop" {
		t.Errorf("name = %q, want laptop", resolved.Name)
	}
}

func TestDeleteRevokesTheToken(t *testing.T) {
	ctx := context.Background()
	store, owner, _ := newStore(t)

	created, token, err := store.Issue(ctx, owner, "laptop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, owner, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Resolve(ctx, token); err == nil {
		t.Fatal("a deleted key still authenticates")
	}
	if err := store.Delete(ctx, owner, created.ID); err == nil {
		t.Error("deleting twice did not report the second as absent")
	}
}

func TestKeysArePerAccountAndCapped(t *testing.T) {
	ctx := context.Background()
	store, owner, stranger := newStore(t)

	for i := 0; i < MaxPerUser; i++ {
		if _, _, err := store.Issue(ctx, owner, "key", 0); err != nil {
			t.Fatalf("issue %d: %v", i+1, err)
		}
	}
	if _, _, err := store.Issue(ctx, owner, "one too many", 0); err == nil {
		t.Fatal("issued past the per-account cap")
	}

	// One account filling its allowance must not spend another's.
	if _, _, err := store.Issue(ctx, stranger, "mine", 0); err != nil {
		t.Fatalf("a second account was blocked by the first: %v", err)
	}

	listed, err := store.List(ctx, stranger)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Errorf("the stranger sees %d keys, want only their own", len(listed))
	}
}
