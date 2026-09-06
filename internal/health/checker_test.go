package health

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

func checkerFixture(t *testing.T) (*Checker, *model.Store, model.Model) {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "health.db"),
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
	models := model.NewStore(db, providers)

	upstream, err := providers.Create(ctx, provider.CreateInput{
		Name: "Upstream", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-test-0123", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := models.Create(ctx, model.CreateInput{
		ProviderID: upstream.ID, ModelID: "vendor/one", DisplayName: "One", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	return &Checker{Store: NewStore(db), Models: models, Providers: providers}, models, record
}

func reload(t *testing.T, models *model.Store, modelID string) model.Model {
	t.Helper()
	record, err := models.ByID(context.Background(), modelID)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// The rule the whole flag exists for. A model an administrator switched off
// has been decided about; a model that starts answering again is not an
// argument against that decision, and re-enabling it would quietly undo an
// operator's word.
func TestOnlyTheSystemsOwnDecisionIsUndone(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()
	policy := Policy{DisableAfter: 2}

	// Switched off by a person: no auto_disabled flag.
	off := false
	if _, err := models.Update(ctx, record.ID, model.Update{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	byHand := reload(t, models, record.ID)

	checker.apply(ctx, byHand, Summarise(record.ID, []Sample{pass(100, "system")}), policy)
	if reload(t, models, record.ID).Enabled {
		t.Error("a model an administrator disabled was switched back on")
	}

	// Switched off by the checker: that one comes back.
	on := true
	if _, err := models.Update(ctx, record.ID, model.Update{AutoDisabled: &on}); err != nil {
		t.Fatal(err)
	}
	byChecker := reload(t, models, record.ID)

	checker.apply(ctx, byChecker, Summarise(record.ID, []Sample{pass(200, "system")}), policy)
	back := reload(t, models, record.ID)
	if !back.Enabled {
		t.Error("a model the checker disabled did not come back")
	}
	if back.AutoDisabled {
		t.Error("the flag survived the model being enabled, so the next manual disable would be undone too")
	}
}

// Failures have to be consecutive and reach the threshold. One bad minute in
// an otherwise good hour is not an outage, and turning a model off for it
// would be worse than the minute.
func TestAModelIsDisabledOnlyAfterTheThreshold(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()
	policy := Policy{DisableAfter: 3}

	twoThenGood := []Sample{fail(400, "timeout", "user"), fail(300, "timeout", "user"), pass(200, "user")}
	checker.apply(ctx, record, Summarise(record.ID, twoThenGood), policy)
	if !reload(t, models, record.ID).Enabled {
		t.Fatal("two failures were enough to disable it")
	}

	three := []Sample{
		fail(700, "timeout", "user"), fail(600, "timeout", "user"), fail(500, "timeout", "user"),
		pass(200, "user"),
	}
	checker.apply(ctx, record, Summarise(record.ID, three), policy)
	after := reload(t, models, record.ID)
	if after.Enabled {
		t.Error("three failures in a row did not disable it")
	}
	if !after.AutoDisabled {
		t.Error("it was disabled without the flag that lets it come back")
	}
}

// Zero is off. An operator who has not asked for this must not find their
// models switched off by it.
func TestWithoutAThresholdNothingIsEverDisabled(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()

	many := []Sample{}
	for i := range 20 {
		many = append(many, fail(int64(1000-i), "upstream_error", "user"))
	}
	checker.apply(ctx, record, Summarise(record.ID, many), Policy{DisableAfter: 0})

	if !reload(t, models, record.ID).Enabled {
		t.Error("a model was disabled with the policy switched off")
	}
}
