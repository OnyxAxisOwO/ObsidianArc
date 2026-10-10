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
	if _, err := store.Create(ctx, in, true); err == nil {
		t.Fatal("Create accepted plain http to a public host with no opt-in")
	}

	in.AllowInsecure = true
	record, err := store.Create(ctx, in, true)
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
	updated, err := store.Update(ctx, record.ID, Update{TimeoutSeconds: &timeout}, false)
	if err != nil {
		t.Fatalf("edit an unrelated field: %v", err)
	}
	if !updated.AllowInsecure {
		t.Error("the opt-in did not survive an edit that never mentioned it")
	}

	// Clearing it is a real change of mind, and the address it was granted
	// for stops being acceptable at the same moment.
	off := false
	if _, err := store.Update(ctx, record.ID, Update{AllowInsecure: &off}, false); err == nil {
		t.Error("Update cleared the opt-in and kept the http address")
	}

	// The opt-in is per provider: the next one starts from refusal.
	_, err = store.Create(ctx, CreateInput{
		Name:    "another",
		Kind:    adapter.KindOpenAI,
		BaseURL: insecureURL,
		APIKey:  "sk-test-key-5678",
		Enabled: true,
	}, true)
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
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	// The shape the admin handler builds for a duplicate: every field the
	// form carried, no key, and the row to take one from.
	copied, err := store.Create(ctx, CreateInput{
		Name: "Primary 2", Kind: source.Kind,
		BaseURL: "https://api.example.com/v1", CopyKeyFrom: source.ID,
		Headers: map[string]string{"X-Title": "Arc backup"}, Enabled: true,
	}, true)
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if copied.ID == source.ID {
		t.Fatal("the copy reused the source's id")
	}
	if copied.Headers["X-Title"] != "Arc backup" {
		t.Errorf("the copy took the source's headers: %v", copied.Headers)
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
	}, true)
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
	}, true)
	if !errors.Is(err, ErrKeyRequired) {
		t.Errorf("gave %v, want ErrKeyRequired", err)
	}
}

// The key goes wherever the base URL points, and nobody can read it back, so
// it is only ever sent to a new address by somebody who typed it again. A
// super administrator is the one who may choose that address at all, so the
// key check is asked of them; it is not what stops a delegate.
func TestTheKeyOnlyGoesSomewhereNewWhenTypedAgain(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	source, err := store.Create(ctx, CreateInput{
		Name: "Primary", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-the-real-secret-value", Enabled: true,
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	moved := "https://collector.example.net/v1"
	if _, err := store.Update(ctx, source.ID, Update{BaseURL: &moved}, true); !errors.Is(err, ErrKeyNeededForMove) {
		t.Errorf("repointing without the key = %v, want ErrKeyNeededForMove", err)
	}
	if _, err := store.Create(ctx, CreateInput{
		Name: "Copy", Kind: adapter.KindOpenAI, BaseURL: moved, CopyKeyFrom: source.ID, Enabled: true,
	}, true); !errors.Is(err, ErrKeyNeededForMove) {
		t.Errorf("copying the key to a new address = %v, want ErrKeyNeededForMove", err)
	}
	if resolved, _ := store.Resolve(ctx, source.ID); resolved.BaseURL != source.BaseURL {
		t.Errorf("the refused edit moved the provider to %q", resolved.BaseURL)
	}

	// With the key typed again it moves, and so does an edit that leaves the
	// address where it was.
	key := "sk-a-new-key-value"
	if _, err := store.Update(ctx, source.ID, Update{BaseURL: &moved, APIKey: &key}, true); err != nil {
		t.Errorf("repointing with the key: %v", err)
	}
	name := "Renamed"
	if _, err := store.Update(ctx, source.ID, Update{Name: &name}, false); err != nil {
		t.Errorf("an edit that does not move the provider: %v", err)
	}
}

// Every chat on a provider is sent to its base URL, prompts and attachments
// included, so the address is the super administrator's to move, as the public
// URL is. Holding the providers grant is not enough, and typing a key of one's
// own does not change that: the refused edit stores neither the address nor
// the key. Saving the address as it is stored, spelled the way it is stored, is
// not a move and stays open to the grant.
func TestOnlyASuperAdministratorMovesTheBaseURL(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	source, err := store.Create(ctx, CreateInput{
		Name: "Primary", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-the-real-secret-value", Enabled: true,
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	moved := "https://collector.example.net/v1"
	typed := "sk-typed-by-a-delegate"
	if _, err := store.Update(ctx, source.ID, Update{BaseURL: &moved, APIKey: &typed}, false); !errors.Is(err, ErrBaseURLNeedsSuperAdmin) {
		t.Fatalf("a delegate moved the base URL with a key of their own: %v, want ErrBaseURLNeedsSuperAdmin", err)
	}
	after, err := store.ByID(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.BaseURL != source.BaseURL || after.APIKeyHint != source.APIKeyHint {
		t.Errorf("the refused move changed the provider: address %q, key hint %q", after.BaseURL, after.APIKeyHint)
	}

	// Validation normalises the address before the comparison, so the stored
	// address written with a trailing slash is the same address.
	respelled := "https://api.example.com/v1/"
	if _, err := store.Update(ctx, source.ID, Update{BaseURL: &respelled}, false); err != nil {
		t.Errorf("a delegate saving the stored address respelled: %v", err)
	}

	if _, err := store.Update(ctx, source.ID, Update{BaseURL: &moved}, true); !errors.Is(err, ErrKeyNeededForMove) {
		t.Errorf("the super administrator moving the address without the key = %v, want ErrKeyNeededForMove", err)
	}
	if _, err := store.Update(ctx, source.ID, Update{BaseURL: &moved, APIKey: &typed}, true); err != nil {
		t.Fatalf("the super administrator moving the address with the key: %v", err)
	}
	if current, err := store.ByID(ctx, source.ID); err != nil || current.BaseURL != moved {
		t.Errorf("after the super administrator's move the address is %q, %v", current.BaseURL, err)
	}
}

// Creating a provider chooses its address, so a delegate's create is refused
// the way a delegate's move is. The one exception is a duplicate at the address
// the source already has: its key is copied inside the database and goes
// nowhere new. A typed key is refused at every address, including one a
// provider already has, because typing a key is choosing where it is sent.
func TestADelegateCanOnlyDuplicateAProviderAtItsOwnAddress(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	source, err := store.Create(ctx, CreateInput{
		Name: "Primary", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-the-real-secret-value", Enabled: true,
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	for _, base := range []string{"https://collector.example.net/v1", "https://api.example.com/v1"} {
		if _, err := store.Create(ctx, CreateInput{
			Name: "Typed", Kind: adapter.KindOpenAI,
			BaseURL: base, APIKey: "sk-typed-by-a-delegate", Enabled: true,
		}, false); !errors.Is(err, ErrBaseURLNeedsSuperAdmin) {
			t.Errorf("a delegate typing a key for %s = %v, want ErrBaseURLNeedsSuperAdmin", base, err)
		}
	}
	if _, err := store.Create(ctx, CreateInput{
		Name: "Copy", Kind: adapter.KindOpenAI, BaseURL: "https://collector.example.net/v1",
		CopyKeyFrom: source.ID, Enabled: true,
	}, false); !errors.Is(err, ErrBaseURLNeedsSuperAdmin) {
		t.Errorf("a delegate copying the key to a new address = %v, want ErrBaseURLNeedsSuperAdmin", err)
	}

	copied, err := store.Create(ctx, CreateInput{
		Name: "Primary 2", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", CopyKeyFrom: source.ID, Enabled: true,
	}, false)
	if err != nil {
		t.Fatalf("a delegate duplicating at the same address: %v", err)
	}
	if resolved, err := store.Resolve(ctx, copied.ID); err != nil || resolved.APIKey != "sk-the-real-secret-value" {
		t.Errorf("the duplicate's key opened as %q, %v", resolved.APIKey, err)
	}

	listed, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Errorf("the refused creates left %d providers, want the source and its copy", len(listed))
	}
}
