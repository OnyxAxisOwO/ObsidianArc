package model

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// A route to an id that names no model is a mistake in the form. It must not
// come back as ErrNotFound: TranslateError reports that as the model being
// saved being unavailable, which sends the operator looking at the wrong row.
func TestRouteToAMissingModelIsReportedAsItsTargetBeingMissing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Example")
	source := f.model(t, upstream.ID, "front-model")

	missing := "no-such-model"
	_, err := f.models.Update(ctx, source.ID, Update{RouteToID: &missing})
	if !errors.Is(err, ErrRouteTargetMissing) {
		t.Fatalf("edit routed to a missing model: err = %v, want ErrRouteTargetMissing", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("edit routed to a missing model: err = %v also matches ErrNotFound", err)
	}

	_, err = f.models.Create(ctx, CreateInput{
		ProviderID:   upstream.ID,
		ModelID:      "new-model",
		DisplayName:  "new-model",
		Enabled:      true,
		RouteToID:    missing,
		Capabilities: Capabilities{SupportsStreaming: true, SupportsSystemPrompt: true},
		Weights:      Weights{InputToken: 1, OutputToken: 1, ReasoningToken: 1},
	})
	if !errors.Is(err, ErrRouteTargetMissing) {
		t.Fatalf("create routed to a missing model: err = %v, want ErrRouteTargetMissing", err)
	}

	var he *httpx.Error
	if got := TranslateError(err); !errors.As(got, &he) || he.Status != http.StatusBadRequest {
		t.Fatalf("TranslateError(%v) = %v, want a 400", err, got)
	}
}
