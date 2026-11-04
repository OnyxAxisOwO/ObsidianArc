package model

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
)

// A misspelt style used to be stored without a word. Resolution then put it
// in place of the provider's valid style, and the OpenAI adapter has no
// branch for it, so reasoning was simply not asked for.
func TestAnUnknownReasoningStyleIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Example")

	_, err := f.models.Create(ctx, CreateInput{
		ProviderID: upstream.ID, ModelID: "typo-model", Enabled: true,
		ReasoningStyle: adapter.ReasoningStyle("openai-effort"),
	})
	if !errors.Is(err, ErrInvalidReasoningStyle) {
		t.Fatalf("create with a misspelt style gave %v, want ErrInvalidReasoningStyle", err)
	}

	for _, style := range []adapter.ReasoningStyle{adapter.ReasoningEffort, ""} {
		if _, err := f.models.Create(ctx, CreateInput{
			ProviderID: upstream.ID, ModelID: "ok-" + string(style), Enabled: true, ReasoningStyle: style,
		}); err != nil {
			t.Fatalf("create with style %q: %v", style, err)
		}
	}

	record := f.model(t, upstream.ID, "edited-model")
	bad := adapter.ReasoningStyle("openai-effort")
	if _, err := f.models.Update(ctx, record.ID, Update{ReasoningStyle: &bad}); !errors.Is(err, ErrInvalidReasoningStyle) {
		t.Fatalf("edit to a misspelt style gave %v, want ErrInvalidReasoningStyle", err)
	}
	if got, _ := f.models.ByID(ctx, record.ID); got.ReasoningStyle != "" {
		t.Fatalf("refused edit stored style %q", got.ReasoningStyle)
	}
}

// A row that already holds a bad style, from before the check, still saves
// when the edit does not touch the style. Re-checking it would lock the row.
func TestAnEditThatLeavesTheStyleAloneIsNotRefusedByAStoredOne(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Example")
	record := f.model(t, upstream.ID, "legacy-model")

	if _, err := f.db.Exec(ctx,
		`UPDATE models SET reasoning_style = ? WHERE id = ?`, "openai-effort", record.ID); err != nil {
		t.Fatal(err)
	}

	renamed := "Renamed"
	if _, err := f.models.Update(ctx, record.ID, Update{DisplayName: &renamed}); err != nil {
		t.Fatalf("an edit that leaves the style alone was refused: %v", err)
	}
}
