package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// addModel writes a model row without the model package, which imports this
// one. Only the columns without a default are set.
func addModel(t *testing.T, s *Store, providerID, modelID string) string {
	t.Helper()
	ctx := context.Background()
	rowID := id.New()
	now := time.Now().UnixMilli()
	if _, err := s.db.Exec(ctx,
		`INSERT INTO models (id, provider_id, model_id, display_name, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`, rowID, providerID, modelID, modelID, now, now); err != nil {
		t.Fatal(err)
	}
	return rowID
}

// A provider whose models are routed into by another provider's models cannot
// be deleted: the cascade would clear those routes, and the sources would be
// served by their own providers without anybody having chosen that. The
// refusal must leave the provider and the route exactly as they were.
func TestDeleteRefusesAProviderThatOthersRouteInto(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	create := func(name string) Provider {
		t.Helper()
		record, err := store.Create(ctx, CreateInput{
			Name: name, Kind: adapter.KindOpenAI,
			BaseURL: "https://api.example.com/v1", APIKey: "sk-" + name + "-test",
			Enabled: true,
		}, true)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	a := create("Target")
	b := create("Source")
	c := create("Unrouted")

	target := addModel(t, store, a.ID, "vendor/target")
	source := addModel(t, store, b.ID, "vendor/source")
	addModel(t, store, c.ID, "vendor/alone")
	if _, err := store.db.Exec(ctx,
		`UPDATE models SET route_to_id = ? WHERE id = ?`, target, source); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ctx, a.ID); !errors.Is(err, ErrRouted) {
		t.Fatalf("Delete of a routed-into provider: err = %v, want ErrRouted", err)
	}
	if _, err := store.ByID(ctx, a.ID); err != nil {
		t.Fatalf("the refused provider is gone: %v", err)
	}
	var route string
	if err := store.db.QueryRow(ctx,
		`SELECT route_to_id FROM models WHERE id = ?`, source).Scan(&route); err != nil {
		t.Fatal(err)
	}
	if route != target {
		t.Errorf("route after a refused delete = %q, want %q", route, target)
	}

	if err := store.Delete(ctx, c.ID); err != nil {
		t.Errorf("a provider with no inbound routes was refused: %v", err)
	}
}
