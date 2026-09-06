package apikey

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

	created, token, err := store.Issue(ctx, owner, "laptop", "", 0)
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

	_, token, err := store.Issue(ctx, owner, "laptop", "", 0)
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
	if _, _, err := store.Issue(ctx, owner, "laptop", "", 0); err != nil {
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
	created, token, err := store.Issue(ctx, owner, "temporary", "", time.Now().Add(time.Hour).UnixMilli())
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
	if _, _, err := store.Issue(ctx, owner, "doomed", "", past); err == nil {
		t.Fatal("a key that was already expired was issued")
	}
}

func TestNameIsRequiredAndBounded(t *testing.T) {
	ctx := context.Background()
	store, owner, _ := newStore(t)

	for _, bad := range []string{"", "   ", strings.Repeat("x", MaxNameChars+1)} {
		if _, _, err := store.Issue(ctx, owner, bad, "", 0); err == nil {
			t.Errorf("issue with name %q succeeded", bad)
		}
	}
}

// Another account's key id must be useless, not merely hard to find: the
// owner is part of the WHERE clause rather than a check beside it.
func TestOneAccountCannotTouchAnothersKey(t *testing.T) {
	ctx := context.Background()
	store, owner, stranger := newStore(t)

	created, token, err := store.Issue(ctx, owner, "laptop", "", 0)
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

	created, token, err := store.Issue(ctx, owner, "laptop", "", 0)
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
		if _, _, err := store.Issue(ctx, owner, "key", "", 0); err != nil {
			t.Fatalf("issue %d: %v", i+1, err)
		}
	}
	if _, _, err := store.Issue(ctx, owner, "one too many", "", 0); err == nil {
		t.Fatal("issued past the per-account cap")
	}

	// One account filling its allowance must not spend another's.
	if _, _, err := store.Issue(ctx, stranger, "mine", "", 0); err != nil {
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

func TestConcurrentIssuesCannotExceedPerAccountCap(t *testing.T) {
	ctx := context.Background()
	store, owner, _ := newStore(t)

	var issued atomic.Int32
	unexpected := make(chan error, 1)
	var workers sync.WaitGroup
	for range MaxPerUser * 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, _, err := store.Issue(ctx, owner, "parallel key", "", 0); err == nil {
				issued.Add(1)
			} else if !errors.Is(err, ErrTooMany) {
				select {
				case unexpected <- err:
				default:
				}
			}
		}()
	}
	workers.Wait()

	select {
	case err := <-unexpected:
		t.Fatalf("unexpected issue error: %v", err)
	default:
	}
	if got := int(issued.Load()); got != MaxPerUser {
		t.Fatalf("issued %d keys, want exactly %d", got, MaxPerUser)
	}
	listed, err := store.List(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != MaxPerUser {
		t.Fatalf("stored %d keys, want %d", len(listed), MaxPerUser)
	}
}

func TestPauseAndResumeKey(t *testing.T) {
	ctx := context.Background()
	store, owner, _ := newStore(t)

	created, token, err := store.Issue(ctx, owner, "laptop", "", 0)
	if err != nil {
		t.Fatal(err)
	}

	// Normal key resolves fine
	if _, err := store.Resolve(ctx, token); err != nil {
		t.Fatalf("active key failed to resolve: %v", err)
	}

	// Pause key
	paused := true
	updated, err := store.Update(ctx, owner, created.ID, Update{Disabled: &paused})
	if err != nil {
		t.Fatalf("pause key: %v", err)
	}
	if !updated.Disabled {
		t.Error("updated.Disabled = false, want true")
	}

	// Paused key returns ErrPaused
	_, err = store.Resolve(ctx, token)
	if !errors.Is(err, ErrPaused) {
		t.Fatalf("resolve returned %v, want ErrPaused", err)
	}

	// Resume key
	resumed := false
	updated, err = store.Update(ctx, owner, created.ID, Update{Disabled: &resumed})
	if err != nil {
		t.Fatalf("resume key: %v", err)
	}
	if updated.Disabled {
		t.Error("updated.Disabled = true, want false")
	}

	// Now resolves again
	resolved, err := store.Resolve(ctx, token)
	if err != nil {
		t.Fatalf("resumed key failed to resolve: %v", err)
	}
	if resolved.ID != created.ID {
		t.Errorf("resolved id = %q, want %q", resolved.ID, created.ID)
	}
}

func TestKeyModelRestriction(t *testing.T) {
	ctx := context.Background()
	store, owner, _ := newStore(t)

	created, _, err := store.Issue(ctx, owner, "restricted", "claude-3-5-sonnet", 0)
	if err != nil {
		t.Fatal(err)
	}
	if created.ModelID != "claude-3-5-sonnet" {
		t.Errorf("ModelID = %q, want claude-3-5-sonnet", created.ModelID)
	}

	// Update model restriction
	newModel := "gpt-4o"
	updated, err := store.Update(ctx, owner, created.ID, Update{ModelID: &newModel})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ModelID != "gpt-4o" {
		t.Errorf("ModelID = %q, want gpt-4o", updated.ModelID)
	}
}

func TestKeyCanRestrictSeveralModelsAndClearTheRestriction(t *testing.T) {
	ctx := context.Background()
	store, owner, _ := newStore(t)

	created, _, err := store.IssueModels(ctx, owner, "multi-model", []string{
		" model-a ", "model-b", "model-a", " ",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(created.ModelIDs, ","), "model-a,model-b"; got != want {
		t.Fatalf("ModelIDs = %q, want %q", got, want)
	}

	listed, err := store.List(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(listed[0].ModelIDs, ","), "model-a,model-b"; got != want {
		t.Fatalf("listed ModelIDs = %q, want %q", got, want)
	}

	cleared := []string{}
	updated, err := store.Update(ctx, owner, created.ID, Update{ModelIDs: &cleared})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.ModelIDs) != 0 || updated.ModelID != "" {
		t.Errorf("cleared restriction = %+v, want no models", updated)
	}
}
