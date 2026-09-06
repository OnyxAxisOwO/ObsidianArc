package provider

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "provider.db"),
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

	box, err := secret.New([]byte("a-test-instance-secret-value"), secret.PurposeProviderKey)
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(db, box)
}

// A plain-http endpoint on a public address is refused until the provider is
// opted into it, and — the reason the opt-in is a stored column rather than a
// check performed once — an unrelated later edit does not re-reject the
// address that was already accepted.
func TestPlainHTTPNeedsThatProvidersOptIn(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	const insecureURL = "http://198.51.100.7:8000/v1"
	in := CreateInput{
		Name:    "self-hosted",
		Kind:    adapter.KindOpenAI,
		BaseURL: insecureURL,
		APIKey:  "sk-test-key-1234",
		Enabled: true,
	}
	if _, err := store.Create(ctx, in); err == nil {
		t.Fatal("Create accepted plain http to a public host with no opt-in")
	}

	in.AllowInsecure = true
	record, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("create with the opt-in: %v", err)
	}
	if record.BaseURL != insecureURL {
		t.Fatalf("stored base URL = %q, want %q", record.BaseURL, insecureURL)
	}

	reloaded, err := store.ByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !reloaded.AllowInsecure {
		t.Fatal("the opt-in did not round-trip through the database")
	}

	timeout := 300
	updated, err := store.Update(ctx, record.ID, Update{TimeoutSeconds: &timeout})
	if err != nil {
		t.Fatalf("edit an unrelated field: %v", err)
	}
	if !updated.AllowInsecure {
		t.Error("the opt-in did not survive an edit that never mentioned it")
	}

	// Clearing it is a real change of mind, and the address it was granted
	// for stops being acceptable at the same moment.
	off := false
	if _, err := store.Update(ctx, record.ID, Update{AllowInsecure: &off}); err == nil {
		t.Error("Update cleared the opt-in and kept the http address")
	}

	// The opt-in is per provider: the next one starts from refusal.
	_, err = store.Create(ctx, CreateInput{
		Name:    "another",
		Kind:    adapter.KindOpenAI,
		BaseURL: insecureURL,
		APIKey:  "sk-test-key-5678",
		Enabled: true,
	})
	if err == nil {
		t.Error("a second provider inherited the first one's opt-in")
	}
}
