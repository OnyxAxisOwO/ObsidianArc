package model

import (
	"context"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
)

// Routing's whole promise is that a user cannot tell it is there. These
// exercise both halves: the request goes to the target, and everything the
// user can observe still comes from the model they picked.
//
// Worth pinning down because nothing else fails when it breaks. A refactor
// that reads resolved.Model where it meant resolved.Upstream sends the wrong
// model id upstream and reports no error at all.

func (f *fixture) route(t *testing.T, fromID, toID string) {
	t.Helper()
	if _, err := f.models.Update(context.Background(), fromID, Update{RouteToID: &toID}); err != nil {
		t.Fatalf("route %s to %s: %v", fromID, toID, err)
	}
}

func TestRouteSendsTheTargetAndReportsTheRequested(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	target := f.model(t, upstream.ID, "cheap-model")
	requested := f.model(t, upstream.ID, "expensive-model")
	f.route(t, requested.ID, target.ID)

	resolved, err := f.models.Authorize(ctx, "", requested.ID, true)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}

	// What leaves the server.
	if resolved.Upstream.ModelID != "cheap-model" {
		t.Errorf("upstream model id = %q, want cheap-model", resolved.Upstream.ModelID)
	}
	if resolved.Upstream.Spec().ModelID != "cheap-model" {
		t.Errorf("spec model id = %q, want cheap-model", resolved.Upstream.Spec().ModelID)
	}

	// What the user sees, and what they are charged against.
	if resolved.Model.ID != requested.ID {
		t.Errorf("reported model = %q, want the requested one", resolved.Model.ID)
	}
	if resolved.Model.ModelID != "expensive-model" {
		t.Errorf("reported model id = %q, want the requested one", resolved.Model.ModelID)
	}
}

func TestNoRouteLeavesUpstreamAsTheRequestedModel(t *testing.T) {
	f := newFixture(t)

	upstream := f.provider(t, "Example")
	plain := f.model(t, upstream.ID, "plain-model")

	resolved, err := f.models.Authorize(context.Background(), "", plain.ID, true)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if resolved.Upstream.ID != resolved.Model.ID {
		t.Fatal("without a route the two must be the same row")
	}
}

// A route can cross providers — moving traffic off an endpoint having a bad
// day is one of the reasons to have routing at all — so the credential used
// must be the target's, not the one the user's model happens to sit behind.
func TestRouteAcrossProvidersUsesTheTargetsProvider(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	primary := f.provider(t, "Primary")
	fallback := f.provider(t, "Fallback")
	target := f.model(t, fallback.ID, "fallback-model")
	requested := f.model(t, primary.ID, "primary-model")
	f.route(t, requested.ID, target.ID)

	resolved, err := f.models.Authorize(ctx, "", requested.ID, true)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if resolved.Provider.ID != fallback.ID {
		t.Errorf("provider = %q, want the target's %q", resolved.Provider.ID, fallback.ID)
	}
	if resolved.Model.ProviderID != primary.ID {
		t.Errorf("reported provider = %q, want the requested model's %q",
			resolved.Model.ProviderID, primary.ID)
	}
}

// A route whose target has been switched off must fail rather than quietly
// falling back to the model the operator was routing away from — that model
// is very often the broken one.
func TestRouteToADisabledModelIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	target := f.model(t, upstream.ID, "target-model")
	requested := f.model(t, upstream.ID, "front-model")
	f.route(t, requested.ID, target.ID)

	disabled := false
	if _, err := f.models.Update(ctx, target.ID, Update{Enabled: &disabled}); err != nil {
		t.Fatalf("disable target: %v", err)
	}

	if _, err := f.models.Authorize(ctx, "", requested.ID, true); err != ErrDisabled {
		t.Fatalf("authorize err = %v, want ErrDisabled", err)
	}
}

// One endpoint can serve a model wanting a thinking budget beside one wanting
// reasoning_effort, so the model's own setting wins.
func TestPerModelReasoningStyleOverridesTheProvider(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	styled := f.model(t, upstream.ID, "styled-model")

	qwen := adapter.ReasoningStyle("qwen")
	if _, err := f.models.Update(ctx, styled.ID, Update{ReasoningStyle: &qwen}); err != nil {
		t.Fatalf("set style: %v", err)
	}

	resolved, err := f.models.Authorize(ctx, "", styled.ID, true)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if resolved.Provider.ReasoningStyle != qwen {
		t.Errorf("reasoning style = %q, want qwen", resolved.Provider.ReasoningStyle)
	}
}

func TestEmptyReasoningStyleInheritsTheProvider(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream, err := f.providers.Create(ctx, provider.CreateInput{
		Name:           "Styled",
		Kind:           adapter.KindOpenAI,
		BaseURL:        "https://api.example.com/v1",
		APIKey:         "sk-test-key-1234",
		Enabled:        true,
		ReasoningStyle: adapter.ReasoningStyle("openai_effort"),
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	plain := f.model(t, upstream.ID, "inherit-model")

	resolved, err := f.models.Authorize(ctx, "", plain.ID, true)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if resolved.Provider.ReasoningStyle != "openai_effort" {
		t.Errorf("reasoning style = %q, want the provider's openai_effort",
			resolved.Provider.ReasoningStyle)
	}
}
