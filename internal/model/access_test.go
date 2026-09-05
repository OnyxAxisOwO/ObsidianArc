package model

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

// Hidden models must be absent from the picker listing for both regular users
// and administrators. The chat picker is for picking directly callable models,
// not internal routing destinations.
func TestHiddenModelNotInListForUser(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	publicModel := f.model(t, upstream.ID, "public-model")

	hidden := true
	hiddenModel, err := f.models.Create(ctx, CreateInput{
		ProviderID:   upstream.ID,
		ModelID:      "hidden-model",
		DisplayName:  "Hidden Model",
		Enabled:      true,
		Hidden:       hidden,
		Capabilities: Capabilities{SupportsStreaming: true, SupportsSystemPrompt: true},
		Weights:      Weights{InputToken: 1, OutputToken: 1, ReasoningToken: 1},
	})
	if err != nil {
		t.Fatalf("create hidden model: %v", err)
	}

	open, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Open", AllowAllModels: true})
	if err != nil {
		t.Fatal(err)
	}

	// Regular user view: hidden model must not appear.
	models, err := f.models.ListForUser(ctx, open.ID, false)
	if err != nil {
		t.Fatalf("list for user: %v", err)
	}
	for _, m := range models {
		if m.ID == hiddenModel.ID {
			t.Fatal("hidden model appeared in ListForUser for user")
		}
	}
	if len(models) != 1 || models[0].ID != publicModel.ID {
		t.Fatalf("got models %+v, want only publicModel", models)
	}

	// Admin view in picker: hidden model must also not appear.
	adminModels, err := f.models.ListForUser(ctx, "", true)
	if err != nil {
		t.Fatalf("list for admin user: %v", err)
	}
	for _, m := range adminModels {
		if m.ID == hiddenModel.ID {
			t.Fatal("hidden model appeared in ListForUser for admin")
		}
	}
}

// A direct request for a hidden model must be refused by Authorize, and the
// error must be indistinguishable from a non-existent or ungranted model to
// prevent enumeration.
func TestHiddenModelAuthorizeRefusedWithSameError(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	hiddenModel, err := f.models.Create(ctx, CreateInput{
		ProviderID:   upstream.ID,
		ModelID:      "hidden-model",
		DisplayName:  "Hidden Model",
		Enabled:      true,
		Hidden:       true,
		Capabilities: Capabilities{SupportsStreaming: true, SupportsSystemPrompt: true},
		Weights:      Weights{InputToken: 1, OutputToken: 1, ReasoningToken: 1},
	})
	if err != nil {
		t.Fatalf("create hidden model: %v", err)
	}

	open, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Open", AllowAllModels: true})
	if err != nil {
		t.Fatal(err)
	}

	_, errHidden := f.models.Authorize(ctx, open.ID, hiddenModel.ID, false)
	if !errors.Is(errHidden, ErrNotPermitted) {
		t.Fatalf("want ErrNotPermitted, got %v", errHidden)
	}

	_, errMissing := f.models.Authorize(ctx, open.ID, "01ARZ3NDEKTSV4RRFFQ69G5FAV", false)
	if errMissing == nil || errMissing.Error() != errHidden.Error() {
		t.Fatalf("hidden model error %v differs from missing model error %v", errHidden, errMissing)
	}
}

// Crucial invariant: a hidden model cannot be asked for directly, but CAN be
// resolved as a route destination when a permitted public model forwards to it.
func TestHiddenModelCanBeRouteTarget(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	targetModel, err := f.models.Create(ctx, CreateInput{
		ProviderID:   upstream.ID,
		ModelID:      "internal-mini",
		DisplayName:  "Internal Mini",
		Enabled:      true,
		Hidden:       true,
		Capabilities: Capabilities{SupportsStreaming: true, SupportsSystemPrompt: true},
		Weights:      Weights{InputToken: 1, OutputToken: 1, ReasoningToken: 1},
	})
	if err != nil {
		t.Fatalf("create target model: %v", err)
	}

	publicModel := f.model(t, upstream.ID, "public-flagship")
	f.route(t, publicModel.ID, targetModel.ID)

	open, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Open", AllowAllModels: true})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := f.models.Authorize(ctx, open.ID, publicModel.ID, false)
	if err != nil {
		t.Fatalf("authorizing route to hidden model failed: %v", err)
	}

	if resolved.Model.ID != publicModel.ID {
		t.Errorf("reported model = %q, want %q", resolved.Model.ID, publicModel.ID)
	}
	if resolved.Upstream.ModelID != "internal-mini" {
		t.Errorf("upstream model = %q, want internal-mini", resolved.Upstream.ModelID)
	}
}

// Tiered group access:
//   - 'use' tier is listed in ListForUser with Usable=true, and Authorize succeeds
//   - 'view' tier is listed in ListForUser with Usable=false, but Authorize fails
//   - no row is omitted from ListForUser and Authorize fails
func TestGroupAccessTiers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	useModel := f.model(t, upstream.ID, "model-use")
	viewModel := f.model(t, upstream.ID, "model-view")
	unlistedModel := f.model(t, upstream.ID, "model-unlisted")

	restricted, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Restricted", AllowAllModels: false})
	if err != nil {
		t.Fatal(err)
	}

	grants := []GroupGrant{
		{ModelID: useModel.ID, Access: AccessUse},
		{ModelID: viewModel.ID, Access: AccessView},
	}
	if err := f.models.SetGroupModels(ctx, restricted.ID, grants); err != nil {
		t.Fatalf("set group models: %v", err)
	}

	// 1. ListForUser check
	models, err := f.models.ListForUser(ctx, restricted.ID, false)
	if err != nil {
		t.Fatalf("list for user: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2 (use and view)", len(models))
	}

	var foundUse, foundView bool
	for _, m := range models {
		if m.ID == useModel.ID {
			foundUse = true
			if !m.Usable {
				t.Errorf("model-use has Usable=false, want true")
			}
		} else if m.ID == viewModel.ID {
			foundView = true
			if m.Usable {
				t.Errorf("model-view has Usable=true, want false")
			}
		} else if m.ID == unlistedModel.ID {
			t.Errorf("unlisted model appeared in ListForUser")
		}
	}
	if !foundUse || !foundView {
		t.Fatalf("missing expected models: use=%v, view=%v", foundUse, foundView)
	}

	// 2. Authorize checks
	// 'use' tier succeeds
	if _, err := f.models.Authorize(ctx, restricted.ID, useModel.ID, false); err != nil {
		t.Errorf("authorize for 'use' model failed: %v", err)
	}

	// 'view' tier is refused with ErrNotPermitted
	_, errView := f.models.Authorize(ctx, restricted.ID, viewModel.ID, false)
	if !errors.Is(errView, ErrNotPermitted) {
		t.Errorf("authorize for 'view' model: want ErrNotPermitted, got %v", errView)
	}

	// unlisted model is refused with ErrNotPermitted
	_, errUnlisted := f.models.Authorize(ctx, restricted.ID, unlistedModel.ID, false)
	if !errors.Is(errUnlisted, ErrNotPermitted) {
		t.Errorf("authorize for unlisted model: want ErrNotPermitted, got %v", errUnlisted)
	}
}

// Groups with allow_all_models bypass explicit grants: all enabled, non-hidden
// models are listed with Usable=true and authorized for turns.
func TestAllowAllModelsBehaviorPreserved(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	upstream := f.provider(t, "Example")
	m1 := f.model(t, upstream.ID, "model-1")
	m2 := f.model(t, upstream.ID, "model-2")

	open, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Open", AllowAllModels: true})
	if err != nil {
		t.Fatal(err)
	}

	models, err := f.models.ListForUser(ctx, open.ID, false)
	if err != nil {
		t.Fatalf("list for user: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	for _, m := range models {
		if !m.Usable {
			t.Errorf("model %s has Usable=false under allow_all_models", m.DisplayName)
		}
	}

	if _, err := f.models.Authorize(ctx, open.ID, m1.ID, false); err != nil {
		t.Errorf("authorize m1: %v", err)
	}
	if _, err := f.models.Authorize(ctx, open.ID, m2.ID, false); err != nil {
		t.Errorf("authorize m2: %v", err)
	}
}
