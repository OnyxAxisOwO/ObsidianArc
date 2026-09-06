package model

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

// The name an instance answers to is the operator's to choose, and it is
// stored beside the upstream's own name rather than replacing it: the request
// still has to go out under what the provider knows.
func TestAnAPINameIsStoredBesideTheModelID(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Upstream")

	record, err := f.models.Create(ctx, CreateInput{
		ProviderID: upstream.ID,
		ModelID:    "gpt-5.6-reasoning",
		APIName:    "gpt-5.6-sol",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.ModelID != "gpt-5.6-reasoning" || record.APIName != "gpt-5.6-sol" {
		t.Fatalf("stored %q / %q", record.ModelID, record.APIName)
	}

	reloaded, err := f.models.ByID(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.APIName != "gpt-5.6-sol" {
		t.Errorf("read back %q, want the API name", reloaded.APIName)
	}

	// Clearing it is how an operator goes back to serving the upstream name,
	// so an empty string has to be a value and not "leave it alone".
	empty := ""
	cleared, err := f.models.Update(ctx, record.ID, Update{APIName: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.APIName != "" {
		t.Errorf("clearing left %q", cleared.APIName)
	}
	if cleared.ModelID != "gpt-5.6-reasoning" {
		t.Errorf("clearing the API name moved the model id to %q", cleared.ModelID)
	}
}

// Two models answering to one name would leave one of them unreachable, and
// which one would depend on a sort order somebody can drag. The index is the
// guarantee; what is checked here is that the refusal says which name is
// taken rather than the message about the provider's own uniqueness.
func TestTwoModelsCannotShareAnAPIName(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Upstream")

	if _, err := f.models.Create(ctx, CreateInput{
		ProviderID: upstream.ID, ModelID: "gpt-5.6-reasoning", APIName: "gpt-5.6-sol",
	}); err != nil {
		t.Fatal(err)
	}

	_, err := f.models.Create(ctx, CreateInput{
		ProviderID: upstream.ID, ModelID: "claude-opus-5", APIName: "gpt-5.6-sol",
	})
	if !errors.Is(err, ErrDuplicateAPIName) {
		t.Fatalf("a second claim gave %v, want ErrDuplicateAPIName", err)
	}

	// And the same on the way in through an edit, which is the likelier way
	// to collide: the name is typed against a list somebody is looking at.
	other, err := f.models.Create(ctx, CreateInput{
		ProviderID: upstream.ID, ModelID: "claude-opus-5",
	})
	if err != nil {
		t.Fatal(err)
	}
	taken := "gpt-5.6-sol"
	if _, err := f.models.Update(ctx, other.ID, Update{APIName: &taken}); !errors.Is(err, ErrDuplicateAPIName) {
		t.Errorf("editing onto a taken name gave %v, want ErrDuplicateAPIName", err)
	}

	// Its own name is not a collision with itself.
	same := "claude-sol"
	if _, err := f.models.Update(ctx, other.ID, Update{APIName: &same}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.models.Update(ctx, other.ID, Update{APIName: &same}); err != nil {
		t.Errorf("saving a model's own API name again failed: %v", err)
	}
}

// Two models with no API name are not two models sharing one: empty is the
// ordinary case and most rows have it.
func TestManyModelsMayHaveNoAPIName(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Upstream")

	for _, modelID := range []string{"one", "two", "three"} {
		if _, err := f.models.Create(ctx, CreateInput{
			ProviderID: upstream.ID, ModelID: modelID,
		}); err != nil {
			t.Fatalf("%s: %v", modelID, err)
		}
	}
}

// An API name goes into a URL path and a client's configuration file, so the
// shapes that would not survive either are refused at the door rather than
// stored and puzzled over later.
func TestAnAPINameIsHeldToTheShapeOfAnIdentifier(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Upstream")

	for _, name := range []string{"gpt 5.6 sol", "gpt\t5.6", "a/b", "a?b", "a#b"} {
		_, err := f.models.Create(ctx, CreateInput{
			ProviderID: upstream.ID, ModelID: "gpt-5.6-reasoning", APIName: name,
		})
		if !errors.Is(err, ErrInvalidAPIName) {
			t.Errorf("%q gave %v, want ErrInvalidAPIName", name, err)
		}
	}

	// The shapes model names actually take are all fine, and surrounding
	// space is trimmed rather than refused — it is a paste, not a decision.
	record, err := f.models.Create(ctx, CreateInput{
		ProviderID: upstream.ID, ModelID: "gpt-5.6-reasoning", APIName: "  gpt-5.6-sol_2:preview  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.APIName != "gpt-5.6-sol_2:preview" {
		t.Errorf("stored %q, want it trimmed", record.APIName)
	}
}

// readCallable spells out its own scan targets rather than going through
// scan(), so the two lists can drift apart in silence — adding api_name to
// the shared column list and not to that hand-written scan turned every
// completion into a 500 with no clue in it. This is the guard: the gateway's
// own read has to carry the field too.
func TestTheGatewaysReadCarriesTheAPIName(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Upstream")

	open, err := f.groups.Create(ctx, nil, group.CreateInput{
		Name: "Open", IsDefault: true, AllowAllModels: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	record, err := f.models.Create(ctx, CreateInput{
		ProviderID: upstream.ID,
		ModelID:    "gpt-5.6-reasoning",
		APIName:    "gpt-5.6-sol",
		Enabled:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := f.models.Authorize(ctx, open.ID, record.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model.APIName != "gpt-5.6-sol" {
		t.Errorf("the callable read gave APIName %q, want the stored name", resolved.Model.APIName)
	}
	// And the name that actually goes upstream is still the upstream's.
	if resolved.Model.ModelID != "gpt-5.6-reasoning" {
		t.Errorf("the request would go out as %q", resolved.Model.ModelID)
	}
}

// A model with nothing to say about how it is talked to gets the instance's
// prompt. One that does gets its own, and the instance's is not appended:
// two voices in one system prompt is how a model ends up ignoring both.
func TestAModelsOwnPromptReplacesTheInstances(t *testing.T) {
	const house = "You are the house assistant."

	if got := (Model{}).Prompt(house); got != house {
		t.Errorf("a model with no prompt got %q", got)
	}
	if got := (Model{SystemPrompt: "   "}).Prompt(house); got != house {
		t.Errorf("a prompt of spaces is not a prompt: %q", got)
	}

	own := "Answer only in valid JSON."
	if got := (Model{SystemPrompt: own}).Prompt(house); got != own {
		t.Errorf("got %q, want the model's own", got)
	}
	// And with no instance prompt either, its own still stands.
	if got := (Model{SystemPrompt: own}).Prompt(""); got != own {
		t.Errorf("got %q", got)
	}
}
