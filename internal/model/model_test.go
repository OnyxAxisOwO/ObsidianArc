package model

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

type fixture struct {
	db        *database.DB
	groups    *group.Store
	providers *provider.Store
	models    *Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "model.db"),
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
	providers := provider.NewStore(db, box)
	return &fixture{
		db:        db,
		groups:    group.NewStore(db),
		providers: providers,
		models:    NewStore(db, providers),
	}
}

func (f *fixture) provider(t *testing.T, name string) provider.Provider {
	t.Helper()
	record, err := f.providers.Create(context.Background(), provider.CreateInput{
		Name:    name,
		Kind:    adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1",
		APIKey:  "sk-test-key-1234",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return record
}

func (f *fixture) model(t *testing.T, providerID, modelID string) Model {
	t.Helper()
	record, err := f.models.Create(context.Background(), CreateInput{
		ProviderID:   providerID,
		ModelID:      modelID,
		DisplayName:  modelID,
		Enabled:      true,
		Capabilities: Capabilities{SupportsStreaming: true, SupportsSystemPrompt: true},
		Weights:      Weights{InputToken: 1, OutputToken: 1, ReasoningToken: 1},
	})
	if err != nil {
		t.Fatalf("create model %s: %v", modelID, err)
	}
	return record
}

// The credential must never be readable from the shape that gets serialised
// to an administrator, and must round-trip through the one path that is
// allowed to decrypt it.
func TestProviderKeyIsEncryptedAndNeverListed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	created := f.provider(t, "Example")
	if created.APIKeyHint == "" || created.APIKeyHint == "sk-test-key-1234" {
		t.Errorf("hint = %q", created.APIKeyHint)
	}

	var stored []byte
	if err := f.db.QueryRow(ctx, `SELECT api_key_enc FROM providers WHERE id = ?`, created.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) == "sk-test-key-1234" {
		t.Fatal("the API key is stored in plaintext")
	}

	resolved, err := f.providers.Resolve(ctx, created.ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.APIKey != "sk-test-key-1234" {
		t.Errorf("resolved key = %q", resolved.APIKey)
	}
}

// Updating a provider without supplying a key must keep the existing one,
// which is what lets the admin form round-trip a provider it never received
// the key for.
func TestUpdateWithoutKeyKeepsIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	created := f.provider(t, "Example")

	name := "Renamed"
	if _, err := f.providers.Update(ctx, created.ID, provider.Update{Name: &name}); err != nil {
		t.Fatalf("update: %v", err)
	}

	resolved, err := f.providers.Resolve(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.APIKey != "sk-test-key-1234" {
		t.Errorf("key changed on an unrelated update: %q", resolved.APIKey)
	}
	if resolved.Name != "Renamed" {
		t.Errorf("name = %q", resolved.Name)
	}
}

func TestProviderRejectsUnsafeBaseURL(t *testing.T) {
	f := newFixture(t)
	_, err := f.providers.Create(context.Background(), provider.CreateInput{
		Name:    "Insecure",
		Kind:    adapter.KindOpenAI,
		BaseURL: "http://api.example.com/v1",
		APIKey:  "sk-test",
	})
	if err == nil {
		t.Fatal("a plain-http remote base URL was accepted for a credential")
	}
}

// A group with explicit grants sees only those; a group with the shortcut
// sees everything enabled. Both are the same query, so the picker and the
// gateway cannot disagree.
func TestListForUserRespectsGroupGrants(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	allowed := f.model(t, upstream.ID, "allowed-model")
	f.model(t, upstream.ID, "forbidden-model")

	restricted, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Restricted"})
	if err != nil {
		t.Fatal(err)
	}
	open, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Open", AllowAllModels: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.models.SetGroupModelIDs(ctx, restricted.ID, []string{allowed.ID}); err != nil {
		t.Fatal(err)
	}

	visible, err := f.models.ListForUser(ctx, restricted.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 1 || visible[0].ID != allowed.ID {
		t.Fatalf("restricted group sees %+v", visible)
	}

	visible, err = f.models.ListForUser(ctx, open.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 2 {
		t.Fatalf("allow-all group sees %d models, want 2", len(visible))
	}
}

// The gateway's authority check. A model the client was never offered must be
// refused by the query, and refused with the same error as one that does not
// exist — otherwise the endpoint enumerates the catalogue.
func TestAuthorizeRefusesModelsOutsideTheGroup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	allowed := f.model(t, upstream.ID, "allowed-model")
	forbidden := f.model(t, upstream.ID, "forbidden-model")

	restricted, _ := f.groups.Create(ctx, nil, group.CreateInput{Name: "Restricted"})
	if err := f.models.SetGroupModelIDs(ctx, restricted.ID, []string{allowed.ID}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.models.Authorize(ctx, restricted.ID, allowed.ID, false); err != nil {
		t.Fatalf("a granted model was refused: %v", err)
	}

	_, err := f.models.Authorize(ctx, restricted.ID, forbidden.ID, false)
	if !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("want ErrNotPermitted, got %v", err)
	}
	_, missing := f.models.Authorize(ctx, restricted.ID, "01ARZ3NDEKTSV4RRFFQ69G5FAV", false)
	if missing == nil || missing.Error() != err.Error() {
		t.Errorf("a nonexistent model gives a different answer than a forbidden one:\n %v\n %v", missing, err)
	}
}

func TestAuthorizeRefusesDisabledModelOrProvider(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	record := f.model(t, upstream.ID, "some-model")
	open, _ := f.groups.Create(ctx, nil, group.CreateInput{Name: "Open", AllowAllModels: true})

	disabled := false
	if _, err := f.models.Update(ctx, record.ID, Update{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.models.Authorize(ctx, open.ID, record.ID, false); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled model: want ErrDisabled, got %v", err)
	}

	enabled := true
	if _, err := f.models.Update(ctx, record.ID, Update{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.providers.Update(ctx, upstream.ID, provider.Update{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.models.Authorize(ctx, open.ID, record.ID, false); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled provider: want ErrDisabled, got %v", err)
	}
}

func TestDisabledProviderKeepsItsModelsDisabled(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	existing := f.model(t, upstream.ID, "existing-model")
	disabled := false
	if _, err := f.providers.Update(ctx, upstream.ID, provider.Update{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}

	stored, err := f.models.ByID(ctx, existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled {
		t.Fatal("disabling the provider left its existing model enabled")
	}

	created := f.model(t, upstream.ID, "new-model")
	if created.Enabled {
		t.Fatal("a model added to a disabled provider started enabled")
	}

	enabled := true
	updated, err := f.models.Update(ctx, existing.ID, Update{Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled {
		t.Fatal("a model under a disabled provider could be enabled")
	}
}

func TestDisablingProviderWhileAddingModelLeavesModelDisabled(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Example")

	start := make(chan struct{})
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	var created Model
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		var err error
		created, err = f.models.Create(ctx, CreateInput{
			ProviderID:  upstream.ID,
			ModelID:     "racing-model",
			DisplayName: "Racing model",
			Enabled:     true,
		})
		errors <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		disabled := false
		_, err := f.providers.Update(ctx, upstream.ID, provider.Update{Enabled: &disabled})
		errors <- err
	}()
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}

	stored, err := f.models.ByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled {
		t.Fatal("the concurrently added model remained enabled")
	}
}

// The guard the health checker leans on: two callers racing the same
// decision against the same enabled model must not both believe they were
// the one that flipped it. The WHERE clause is what makes this safe with no
// separate lock — the check and the write are one statement, so only the
// caller whose write actually finds the row still enabled changes anything.
func TestDisableIfEnabledFlipsExactlyOnceUnderConcurrency(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Example")
	record := f.model(t, upstream.ID, "flaky-model")

	const racers = 8
	start := make(chan struct{})
	flips := make(chan bool, racers)
	errs := make(chan error, racers)
	var wg sync.WaitGroup
	wg.Add(racers)
	for range racers {
		go func() {
			defer wg.Done()
			<-start
			flipped, err := f.models.DisableIfEnabled(ctx, record.ID)
			flips <- flipped
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(flips)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	trueCount := 0
	for flipped := range flips {
		if flipped {
			trueCount++
		}
	}
	if trueCount != 1 {
		t.Fatalf("%d of %d racing calls reported flipping the model, want exactly 1", trueCount, racers)
	}

	stored, err := f.models.ByID(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled || !stored.AutoDisabled {
		t.Fatalf("model = %+v, want it disabled with the auto-disabled flag set", stored)
	}
}

// The checker's claim on a model ends with the first decision an operator makes
// about its state. Left standing, the next sweep switches the model back on,
// because the flag still says the checker turned it off.
func TestAnOperatorsStateChangeRetiresTheCheckersFlag(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Example")
	record := f.model(t, upstream.ID, "flaky-model")

	if flipped, err := f.models.DisableIfEnabled(ctx, record.ID); err != nil || !flipped {
		t.Fatalf("the checker's disable: flipped=%v, err=%v", flipped, err)
	}

	on, off := true, false
	if _, err := f.models.Update(ctx, record.ID, Update{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	stored, err := f.models.ByID(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled || stored.AutoDisabled {
		t.Fatalf("after the operator enabled it: %+v, want enabled with no checker flag", stored)
	}

	if _, err := f.models.Update(ctx, record.ID, Update{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	stored, err = f.models.ByID(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled || stored.AutoDisabled {
		t.Fatalf("after the operator disabled it: %+v, want disabled with no checker flag", stored)
	}
}

// The editor sends every field on every save, the enabled one included. A save
// that leaves the state where it was is no decision about it, so it must not
// end the checker's claim: a model the system switched off still comes back on
// its own once it answers.
func TestASaveThatKeepsTheStateKeepsTheFlag(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Example")
	record := f.model(t, upstream.ID, "flaky-model")

	if flipped, err := f.models.DisableIfEnabled(ctx, record.ID); err != nil || !flipped {
		t.Fatalf("the checker's disable: flipped=%v, err=%v", flipped, err)
	}

	name, off := "Corrected", false
	if _, err := f.models.Update(ctx, record.ID, Update{DisplayName: &name, Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	stored, err := f.models.ByID(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DisplayName != "Corrected" {
		t.Fatalf("the save did not land: display name %q", stored.DisplayName)
	}
	if stored.Enabled || !stored.AutoDisabled {
		t.Fatalf("a save that kept the state cleared the checker's flag: %+v", stored)
	}
}

// Under a disabled provider an enable is refused and the model stays off, so
// nothing about it changed and the checker keeps its claim on it. Clearing the
// flag here would strand the model, because enabling a provider again does not
// switch its models back on.
func TestAnEnableRefusedByTheProviderKeepsTheFlag(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Example")
	record := f.model(t, upstream.ID, "flaky-model")

	if flipped, err := f.models.DisableIfEnabled(ctx, record.ID); err != nil || !flipped {
		t.Fatalf("the checker's disable: flipped=%v, err=%v", flipped, err)
	}
	disabled := false
	if _, err := f.providers.Update(ctx, upstream.ID, provider.Update{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}

	on := true
	if _, err := f.models.Update(ctx, record.ID, Update{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	stored, err := f.models.ByID(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled || !stored.AutoDisabled {
		t.Fatalf("a refused enable changed the model: %+v, want it off with the checker's flag", stored)
	}
}

// Authorize returns everything a turn needs, including the decrypted
// credential, in one query.
func TestAuthorizeResolvesTheProvider(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	record := f.model(t, upstream.ID, "some-model")
	open, _ := f.groups.Create(ctx, nil, group.CreateInput{Name: "Open", AllowAllModels: true})

	resolved, err := f.models.Authorize(ctx, open.ID, record.ID, false)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if resolved.Provider.APIKey != "sk-test-key-1234" {
		t.Errorf("provider key = %q", resolved.Provider.APIKey)
	}
	if resolved.Provider.Kind != adapter.KindOpenAI || resolved.Provider.BaseURL != "https://api.example.com/v1" {
		t.Errorf("provider = %+v", resolved.Provider)
	}
	if resolved.Model.Spec().ModelID != "some-model" {
		t.Errorf("spec = %+v", resolved.Model.Spec())
	}
}

// Deleting a provider must take its models — and their grants — with it,
// rather than leaving rows pointing at nothing.
func TestDeletingProviderCascades(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	record := f.model(t, upstream.ID, "some-model")
	open, _ := f.groups.Create(ctx, nil, group.CreateInput{Name: "Open"})
	if err := f.models.SetGroupModelIDs(ctx, open.ID, []string{record.ID}); err != nil {
		t.Fatal(err)
	}

	if err := f.providers.Delete(ctx, upstream.ID); err != nil {
		t.Fatal(err)
	}

	var models, grants int
	_ = f.db.QueryRow(ctx, `SELECT COUNT(*) FROM models`).Scan(&models)
	_ = f.db.QueryRow(ctx, `SELECT COUNT(*) FROM group_models`).Scan(&grants)
	if models != 0 || grants != 0 {
		t.Errorf("after deleting the provider: %d models, %d grants remain", models, grants)
	}
}

func TestDuplicateModelPerProviderIsAllowed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	first := f.provider(t, "First")
	second := f.provider(t, "Second")
	m1 := f.model(t, first.ID, "shared-model")

	// The same upstream model behind a second provider is legitimate: a
	// primary and a fallback.
	f.model(t, second.ID, "shared-model")

	// Multiple entries for the same model under the same provider are also
	// allowed (e.g. different system prompts, reasoning tiers, or weights).
	m2, err := f.models.Create(ctx, CreateInput{
		ProviderID:  first.ID,
		ModelID:     "shared-model",
		DisplayName: "Duplicate",
	})
	if err != nil {
		t.Fatalf("creating duplicate model under same provider: %v", err)
	}
	if m2.ID == m1.ID {
		t.Fatalf("expected different IDs for duplicate model entries, got %q", m2.ID)
	}
}

func TestCreditsUseTheModelWeights(t *testing.T) {
	record := Model{Weights: Weights{
		Request:        0.5,
		InputToken:     1,
		OutputToken:    3,
		ReasoningToken: 2,
	}}

	got := record.Credits(adapter.Usage{InputTokens: 1000, OutputTokens: 2000, ReasoningTokens: 500})
	want := 0.5 + 1.0 + 6.0 + 1.0
	if got != want {
		t.Errorf("Credits = %v, want %v", got, want)
	}

	// A model left at its defaults must cost something, or the ledger records
	// zeroes forever.
	neutral := Model{Weights: Weights{InputToken: 1, OutputToken: 1, ReasoningToken: 1}}
	if neutral.Credits(adapter.Usage{InputTokens: 1000, OutputTokens: 1000}) != 2 {
		t.Error("neutral weights did not price a request")
	}
}

// A negative weight would let a model refund credits.
func TestWeightsAreClamped(t *testing.T) {
	f := newFixture(t)
	upstream := f.provider(t, "Example")

	record, err := f.models.Create(context.Background(), CreateInput{
		ProviderID:  upstream.ID,
		ModelID:     "odd-weights",
		DisplayName: "Odd",
		Weights:     Weights{Request: -5, InputToken: -1, OutputToken: 1e9, ReasoningToken: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Request < 0 || record.InputToken < 0 {
		t.Errorf("negative weights survived: %+v", record.Weights)
	}
	if record.OutputToken > 10000 {
		t.Errorf("an absurd weight was not capped: %v", record.OutputToken)
	}
}

func TestWorstCaseCapsAtDefaultMaxOutput(t *testing.T) {
	// Zero/unset max output tokens falls back to DefaultMaxOutput (4096).
	mZero := Model{
		Capabilities: Capabilities{MaxOutputTokens: 0},
		Weights:      Weights{Request: 1, OutputToken: 2},
	}
	tokens, credits := mZero.WorstCase()
	if tokens != DefaultMaxOutput {
		t.Errorf("got tokens %d, want %d", tokens, DefaultMaxOutput)
	}
	if wantCredits := 1.0 + float64(DefaultMaxOutput)/1000*2.0; credits != wantCredits {
		t.Errorf("got credits %v, want %v", credits, wantCredits)
	}

	// Smaller limits below DefaultMaxOutput are honored.
	mSmall := Model{
		Capabilities: Capabilities{MaxOutputTokens: 1000},
		Weights:      Weights{Request: 0, OutputToken: 1.5},
	}
	tokens, credits = mSmall.WorstCase()
	if tokens != 1000 {
		t.Errorf("got tokens %d, want 1000", tokens)
	}
	if credits != 1.5 {
		t.Errorf("got credits %v, want 1.5", credits)
	}

	// Large limits (e.g. 256k) are capped at DefaultMaxOutput (4096) for reservation.
	mLarge := Model{
		Capabilities: Capabilities{MaxOutputTokens: 256004},
		Weights:      Weights{Request: 1, OutputToken: 1},
	}
	tokens, credits = mLarge.WorstCase()
	if tokens != DefaultMaxOutput {
		t.Errorf("got tokens %d, want %d", tokens, DefaultMaxOutput)
	}
	if wantCredits := 1.0 + float64(DefaultMaxOutput)/1000*1.0; credits != wantCredits {
		t.Errorf("got credits %v, want %v", credits, wantCredits)
	}
}
