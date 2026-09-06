package server

import (
	"net/http"
	"testing"
)

// An exported catalogue names everything — the provider, the route target,
// the groups — because a ULID means nothing on the instance the file is being
// carried to. This is the whole round trip through the handler: names in,
// rows out, and the names resolved against what is actually here.
func TestImportingACatalogueResolvesNamesAndIsRepeatable(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	created := in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Upstream", "kind": "openai",
		"base_url": "https://api.example.com/v1", "api_key": "sk-test-key-0123",
	}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create provider: %d %s", created.Code, created.Body.String())
	}

	file := []any{
		map[string]any{
			"provider": "Upstream", "model_id": "vendor/big", "display_name": "Big",
			"enabled": true, "input_token_weight": 3.0, "supports_reasoning": true,
			// Points at a model further down the file, which does not exist
			// yet when this entry is read.
			"route_to": map[string]any{"provider": "Upstream", "model_id": "vendor/small"},
			"groups":   []any{map[string]any{"group": "Default", "access": "use"}},
		},
		map[string]any{
			"provider": "Upstream", "model_id": "vendor/small", "display_name": "Small",
			"enabled": true,
		},
		map[string]any{
			"provider": "Somewhere Else", "model_id": "vendor/big", "display_name": "Elsewhere",
		},
	}

	first := in.do(http.MethodPost, "/api/admin/models/import", map[string]any{"models": file}, admin)
	if first.Code != http.StatusOK {
		t.Fatalf("import: %d %s", first.Code, first.Body.String())
	}
	result := decode[map[string]any](t, first)
	if result["created"] != 2.0 || result["updated"] != 0.0 {
		t.Errorf("first import: %+v, want 2 created", result)
	}
	skipped, _ := result["skipped"].([]any)
	if len(skipped) != 1 {
		t.Fatalf("skipped %v, want the one naming a provider that is not here", skipped)
	}

	listing := decode[map[string]any](t, in.do(http.MethodGet, "/api/admin/models", nil, admin))
	models, _ := listing["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("listed %d models, want 2", len(models))
	}
	byName := map[string]map[string]any{}
	for _, entry := range models {
		row, _ := entry.(map[string]any)
		byName[row["display_name"].(string)] = row
	}
	big, small := byName["Big"], byName["Small"]
	if big == nil || small == nil {
		t.Fatalf("the two models did not land: %v", byName)
	}
	// The route was named, and the name was resolved to the row the second
	// entry created — which is the pass that exists for exactly this.
	if big["route_to_id"] != small["id"] {
		t.Errorf("route = %v, want %v", big["route_to_id"], small["id"])
	}
	if big["input_token_weight"] != 3.0 || big["supports_reasoning"] != true {
		t.Errorf("the settings did not travel: %+v", big)
	}

	// The same file again is an update of the same rows, not a second copy of
	// them: an import is keyed on the provider and the upstream id.
	again := decode[map[string]any](t, in.do(http.MethodPost, "/api/admin/models/import",
		map[string]any{"models": file}, admin))
	if again["created"] != 0.0 || again["updated"] != 2.0 {
		t.Errorf("second import: %+v, want 0 created and 2 updated", again)
	}

	// And it did not make this instance a mirror of the file. A model the
	// file does not mention stays.
	third := decode[map[string]any](t, in.do(http.MethodPost, "/api/admin/models/import",
		map[string]any{"models": file[1:2]}, admin))
	if third["updated"] != 1.0 {
		t.Errorf("a one-entry file: %+v, want 1 updated", third)
	}
	after := decode[map[string]any](t, in.do(http.MethodGet, "/api/admin/models", nil, admin))
	if rows, _ := after["models"].([]any); len(rows) != 2 {
		t.Errorf("importing one entry left %d models, want both still here", len(rows))
	}
}

// A file with nothing in it is a mistake, not an instruction to do nothing.
func TestAnEmptyCatalogueIsRefused(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	response := in.do(http.MethodPost, "/api/admin/models/import", map[string]any{"models": []any{}}, admin)
	if response.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", response.Code, response.Body.String())
	}
}
