package model

import (
	"context"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
)

// A model with no tiers of its own behaves exactly as it did before they
// existed: the three the client has built in, and a budget the adapter
// derives from the effort.
func TestResolveTierWithoutAList(t *testing.T) {
	var record Model

	effort, budget := record.ResolveTier(adapter.EffortLow)
	if effort != adapter.EffortLow || budget != 0 {
		t.Errorf("low resolved to (%q, %d), want (low, 0)", effort, budget)
	}

	// Anything that is not one of the three is the middle one, which is what
	// the gateway used to do with an unparseable effort.
	effort, budget = record.ResolveTier(adapter.Effort("deep"))
	if effort != adapter.EffortMedium || budget != 0 {
		t.Errorf("an unknown effort resolved to (%q, %d), want (medium, 0)", effort, budget)
	}
}

func TestResolveTierAgainstTheModelsOwnList(t *testing.T) {
	record := Model{ReasoningTiers: []ReasoningTier{
		{ID: "quick", Name: "快速", Budget: 1500},
		{ID: "normal", Name: "标准"},
		{ID: "deep", Name: "深入", Budget: 30000},
	}}

	effort, budget := record.ResolveTier(adapter.Effort("deep"))
	if effort != adapter.Effort("deep") || budget != 30000 {
		t.Errorf("deep resolved to (%q, %d), want (deep, 30000)", effort, budget)
	}

	// A tier with no budget of its own leaves the derivation to the adapter.
	if _, budget = record.ResolveTier(adapter.Effort("normal")); budget != 0 {
		t.Errorf("a tier with no budget resolved to %d, want 0", budget)
	}

	// "medium" is one of the built-in three and means nothing here: a
	// browser carrying it from another model lands on the middle tier
	// rather than failing the turn.
	effort, budget = record.ResolveTier(adapter.EffortMedium)
	if effort != adapter.Effort("normal") || budget != 0 {
		t.Errorf("an effort this model never offered resolved to (%q, %d), want (normal, 0)", effort, budget)
	}
}

// The list reaches the database and comes back, and the rows that could not
// be shown or told apart are dropped on the way in.
func TestReasoningTiersRoundTripAndNormalize(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "tiers")

	tiers := []ReasoningTier{
		{ID: "quick", Name: "快速", Budget: 1500},
		{ID: " deep ", Name: "深入", Budget: 30000},
		{ID: "quick", Name: "第二个 quick"}, // same id: unreachable behind the first
		{ID: "", Name: "无值"},             // nothing to send upstream
		{ID: "blank", Name: "  "},        // a blank stop on the slider
		{ID: "negative", Name: "负数", Budget: -5},
	}

	record, err := f.models.Create(ctx, CreateInput{
		ProviderID:     upstream.ID,
		ModelID:        "custom-tiers",
		DisplayName:    "Custom tiers",
		Enabled:        true,
		ReasoningTiers: tiers,
		Capabilities:   Capabilities{SupportsReasoning: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	reloaded, err := f.models.ByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	got := reloaded.ReasoningTiers
	if len(got) != 3 {
		t.Fatalf("stored %d tiers, want 3: %+v", len(got), got)
	}
	if got[1].ID != "deep" {
		t.Errorf("tier id was not trimmed: %q", got[1].ID)
	}
	if got[2].Budget != 0 {
		t.Errorf("a negative budget survived as %d", got[2].Budget)
	}

	// Emptying the list is how an administrator goes back to the built-in
	// three, so it has to clear the column rather than be ignored as absent.
	none := []ReasoningTier{}
	cleared, err := f.models.Update(ctx, record.ID, Update{ReasoningTiers: &none})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if len(cleared.ReasoningTiers) != 0 {
		t.Errorf("clearing left %+v", cleared.ReasoningTiers)
	}
	reloaded, err = f.models.ByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("read back after clearing: %v", err)
	}
	if len(reloaded.ReasoningTiers) != 0 {
		t.Errorf("the cleared list came back as %+v", reloaded.ReasoningTiers)
	}
}

func TestReasoningTiersAreCapped(t *testing.T) {
	long := make([]ReasoningTier, 0, MaxReasoningTiers+4)
	for i := range MaxReasoningTiers + 4 {
		long = append(long, ReasoningTier{ID: string(rune('a' + i)), Name: "tier"})
	}
	if got := len(normalizeTiers(long)); got != MaxReasoningTiers {
		t.Errorf("kept %d tiers, want the cap of %d", got, MaxReasoningTiers)
	}
}
