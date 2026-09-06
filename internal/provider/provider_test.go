package provider

import (
	"context"
	"errors"
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

// Duplicating a provider carries its credentials. That is most of the reason
// to duplicate one — the same account reached at a second base URL, or a
// second entry for the same endpoint — and the browser asking for the copy
// has never been given the key, so it cannot send one.
func TestACopyCarriesTheKeyWithoutEverUnsealingIt(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	source, err := store.Create(ctx, CreateInput{
		Name: "Primary", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-the-real-secret-value",
		Headers: map[string]string{"X-Title": "Arc"},
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The shape the admin handler builds for a duplicate: every field the
	// form carried, no key, and the row to take one from.
	copied, err := store.Create(ctx, CreateInput{
		Name: "Primary 2", Kind: source.Kind,
		BaseURL: "https://backup.example.com/v1", CopyKeyFrom: source.ID,
		Headers: source.Headers, Enabled: true,
	})
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if copied.ID == source.ID {
		t.Fatal("the copy reused the source's id")
	}
	if copied.BaseURL != "https://backup.example.com/v1" {
		t.Errorf("the copy took the source's base URL: %q", copied.BaseURL)
	}
	// The hint travels with the key, so the form can say which key this is
	// rather than showing an empty field beside a working provider.
	if copied.APIKeyHint == "" || copied.APIKeyHint != source.APIKeyHint {
		t.Errorf("hint = %q, want the source's %q", copied.APIKeyHint, source.APIKeyHint)
	}

	// The proof is that it still opens: the ciphertext was moved by the
	// database, and a byte wrong anywhere in that path fails here rather than
	// on the first request an operator makes in production.
	resolved, err := store.Resolve(ctx, copied.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.APIKey != "sk-the-real-secret-value" {
		t.Errorf("the copy's key opened as %q", resolved.APIKey)
	}
}

// A copy of a provider that is not there must not become a provider with no
// key: an INSERT ... SELECT that matches nothing inserts nothing and reports
// no error, so the row count is the only thing that notices.
func TestCopyingFromAProviderThatIsGoneIsRefused(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	_, err := store.Create(ctx, CreateInput{
		Name: "Orphan", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", CopyKeyFrom: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Enabled: true,
	})
	if err == nil {
		t.Fatal("a copy of nothing was created")
	}

	listed, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Errorf("it left a row behind: %+v", listed)
	}
}

// Neither a key nor a source is still an error: the two spellings are an
// either/or, not a way to make the key optional.
func TestAProviderStillNeedsAKeyFromSomewhere(t *testing.T) {
	store := newStore(t)

	_, err := store.Create(context.Background(), CreateInput{
		Name: "Keyless", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", Enabled: true,
	})
	if !errors.Is(err, ErrKeyRequired) {
		t.Errorf("gave %v, want ErrKeyRequired", err)
	}
}
