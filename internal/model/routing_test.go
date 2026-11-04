package model

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
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
	}, true)
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

// Deleting a route's target used to clear the route through the foreign key,
// and the source then answered from its own provider with no error at all.
// The delete is refused until the routes into the target are gone.
func TestDeletingARouteTargetIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	target := f.model(t, upstream.ID, "target-model")
	source := f.model(t, upstream.ID, "front-model")
	f.route(t, source.ID, target.ID)

	if err := f.models.Delete(ctx, target.ID); !errors.Is(err, ErrRouted) {
		t.Fatalf("delete of a routed target gave %v, want ErrRouted", err)
	}
	if _, err := f.models.ByID(ctx, target.ID); err != nil {
		t.Fatalf("the refused target is gone: %v", err)
	}
	if got, _ := f.models.ByID(ctx, source.ID); got.RouteToID != target.ID {
		t.Fatalf("the source's route became %q after a refused delete", got.RouteToID)
	}

	// Once the operator has cleared the route, the target can go.
	none := ""
	if _, err := f.models.Update(ctx, source.ID, Update{RouteToID: &none}); err != nil {
		t.Fatal(err)
	}
	if err := f.models.Delete(ctx, target.ID); err != nil {
		t.Fatalf("delete after clearing the route: %v", err)
	}
}

// A provider cascades its models away, so a route from another provider into
// one of them is refused for the same reason a model delete is.
func TestDeletingAProviderWithRoutedModelsIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	elsewhere := f.provider(t, "Elsewhere")
	upstream := f.provider(t, "Upstream")
	target := f.model(t, upstream.ID, "target-model")
	source := f.model(t, elsewhere.ID, "front-model")
	f.route(t, source.ID, target.ID)

	if err := f.providers.Delete(ctx, upstream.ID); !errors.Is(err, provider.ErrRouted) {
		t.Fatalf("delete of a routed provider gave %v, want provider.ErrRouted", err)
	}
	if got, _ := f.models.ByID(ctx, source.ID); got.RouteToID != target.ID {
		t.Fatalf("the source's route became %q after a refused provider delete", got.RouteToID)
	}
	if err := f.providers.Delete(ctx, elsewhere.ID); err != nil {
		t.Fatalf("deleting the provider that holds only the source: %v", err)
	}
}

// Route writes and deletes of the same target race on the target's row lock.
// Whichever commits first, the route can never be left dangling: when the
// source still points at the target, the target still exists. Runs on Postgres
// as well when a DSN is named, because SQLite serialises the writers and the
// two cannot interleave there.
func TestRouteWritesRaceDeletesOfTheirTarget(t *testing.T) {
	f := newFixtureOn(t, dbtest.Either(t, filepath.Join(t.TempDir(), "route-delete-race.db")))
	ctx := context.Background()
	upstream := f.provider(t, "Example")

	for round := 0; round < 20; round++ {
		target := f.model(t, upstream.ID, "target-model-"+strconv.Itoa(round))
		source := f.model(t, upstream.ID, "front-model-"+strconv.Itoa(round))

		start := make(chan struct{})
		var routeErr, deleteErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			toID := target.ID
			_, routeErr = f.models.Update(ctx, source.ID, Update{RouteToID: &toID})
		}()
		go func() {
			defer wg.Done()
			<-start
			deleteErr = f.models.Delete(ctx, target.ID)
		}()
		close(start)
		wg.Wait()
		// Both succeeding is the one outcome that clears the route: the route
		// committed, the delete went through, and the foreign key cleared it.
		// Delete refuses a routed target and a route to a deleted one is
		// refused, so one of the two must fail.
		if routeErr == nil && deleteErr == nil {
			t.Fatalf("round %d: the route and the delete both succeeded, so the route was cleared", round)
		}

		got, err := f.models.ByID(ctx, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.RouteToID == target.ID {
			if _, err := f.models.ByID(ctx, target.ID); err != nil {
				t.Fatalf("round %d: the source routes to a target that is gone", round)
			}
		}
	}
}

// Two routes that point at each other across providers are the case the lock
// order in Update exists for. Each Update holds one model row and asks for the
// other, so taken source-first they deadlock on PostgreSQL. Whichever commits
// first lands, the other is refused as a second hop, and neither may come back
// as a deadlock. Runs on Postgres as well when a DSN is named, because SQLite
// serialises the writers and cannot show the cycle.
func TestOppositeRoutesAcrossProvidersQueueInsteadOfDeadlocking(t *testing.T) {
	f := newFixtureOn(t, dbtest.Either(t, filepath.Join(t.TempDir(), "model-opposite-routes.db")))
	ctx := context.Background()
	left := f.provider(t, "Left")
	right := f.provider(t, "Right")

	for round := 0; round < 20; round++ {
		a := f.model(t, left.ID, "a-"+strconv.Itoa(round))
		b := f.model(t, right.ID, "b-"+strconv.Itoa(round))

		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			toB := b.ID
			_, errs[0] = f.models.Update(ctx, a.ID, Update{RouteToID: &toB})
		}()
		go func() {
			defer wg.Done()
			<-start
			toA := a.ID
			_, errs[1] = f.models.Update(ctx, b.ID, Update{RouteToID: &toA})
		}()
		close(start)
		wg.Wait()

		landed := 0
		for _, err := range errs {
			switch {
			case err == nil:
				landed++
			case errors.Is(err, ErrRouteChain):
			default:
				t.Fatalf("round %d: an opposite route failed with %v, want a success or ErrRouteChain", round, err)
			}
		}
		if landed != 1 {
			t.Fatalf("round %d: %d of two opposite routes landed, want exactly one", round, landed)
		}
	}
}
