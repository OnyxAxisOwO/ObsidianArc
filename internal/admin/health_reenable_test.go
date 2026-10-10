package admin

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

// A reset re-enables the models the checker switched off on its own. A write
// that fails must be visible: the operator asked for a clean start, and a
// model left disabled is indistinguishable from one that was already healthy
// if all the response carries is a count of the ones that worked.
func TestAModelThatCouldNotBeReenabledIsNamed(t *testing.T) {
	const (
		offOne = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
		onOne  = "01ARZ3NDEKTSV4RRFFQ69G5FAA"
		offTwo = "01ARZ3NDEKTSV4RRFFQ69G5FAB"
	)
	rows := []model.Model{
		{ID: offOne, AutoDisabled: true},
		{ID: onOne, AutoDisabled: false},
		{ID: offTwo, AutoDisabled: true},
	}

	var asked []string
	update := func(_ context.Context, id string, in model.Update) (model.Model, error) {
		asked = append(asked, id)
		// Every attempt must clear the checker's switch as well as turning the
		// model back on; one without the other leaves it disabled in the list.
		if in.Enabled == nil || !*in.Enabled {
			t.Errorf("update for %s did not enable it: %+v", id, in)
		}
		if in.AutoDisabled == nil || *in.AutoDisabled {
			t.Errorf("update for %s left auto_disabled set: %+v", id, in)
		}
		if id == offTwo {
			return model.Model{}, errors.New("write refused")
		}
		return model.Model{ID: id}, nil
	}

	reenabled, refused := reenableAutoDisabled(context.Background(), rows, update)

	if reenabled != 1 {
		t.Errorf("reenabled = %d, want only the write that succeeded", reenabled)
	}
	if len(refused) != 1 || refused[0] != offTwo {
		t.Errorf("refused = %v, want just %s", refused, offTwo)
	}
	// A model that was never switched off is not written to at all.
	if len(asked) != 2 {
		t.Errorf("wrote to %v, want only the two auto-disabled models", asked)
	}
}

// The ordinary case must stay silent, so the response does not carry a field
// that is always present and always empty.
func TestAResetThatReenablesEverythingNamesNothing(t *testing.T) {
	rows := []model.Model{{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", AutoDisabled: true}}
	update := func(_ context.Context, id string, _ model.Update) (model.Model, error) {
		return model.Model{ID: id}, nil
	}

	reenabled, refused := reenableAutoDisabled(context.Background(), rows, update)
	if reenabled != 1 || len(refused) != 0 {
		t.Errorf("reenabled = %d, refused = %v, want 1 and nothing refused", reenabled, refused)
	}
}

// The reset is the other place the checker's switch gets spent. A model an
// operator has since switched on and then off must stay off through it: the
// reset reads the flag, so the flag has to say what the operator last decided.
func TestAResetLeavesAModelAnOperatorSwitchedOff(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "health-reset.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
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

	// The checker turns the model off, and then an operator switches it on and
	// off again.
	if flipped, err := models.DisableIfEnabled(ctx, record.ID); err != nil || !flipped {
		t.Fatalf("the checker's disable: flipped=%v, err=%v", flipped, err)
	}
	on, off := true, false
	if _, err := models.Update(ctx, record.ID, model.Update{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.Update(ctx, record.ID, model.Update{Enabled: &off}); err != nil {
		t.Fatal(err)
	}

	rows, err := models.ListAll(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	reenabled, refused := reenableAutoDisabled(ctx, rows, models.Update)
	if reenabled != 0 || len(refused) != 0 {
		t.Errorf("the reset re-enabled %d models and refused %v, want none", reenabled, refused)
	}
	stored, err := models.ByID(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled {
		t.Error("a health reset switched on a model an operator had switched off")
	}
}
