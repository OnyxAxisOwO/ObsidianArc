package server

import (
	"net/http"
	"testing"
)

// A weight is what a request costs. A file entry that leaves a weight out used
// to write zero over the model's own, which made the model free to every group
// using it, and a new model from such an entry was free from the start.
func TestImportWithoutAWeightDoesNotMakeTheModelFree(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	if res := in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Upstream", "kind": "openai",
		"base_url": "https://api.example.com/v1", "api_key": "sk-test-key-0123",
	}, admin); res.Code != http.StatusCreated {
		t.Fatalf("create provider: %d %s", res.Code, res.Body.String())
	}

	priced := map[string]any{
		"provider": "Upstream", "model_id": "vendor/priced", "display_name": "Priced", "enabled": true,
		"request_weight": 0.5, "input_token_weight": 3.0, "output_token_weight": 2.0, "reasoning_token_weight": 1.0,
	}
	if res := in.do(http.MethodPost, "/api/admin/models/import", map[string]any{"models": []any{priced}}, admin); res.Code != http.StatusOK {
		t.Fatalf("first import: %d %s", res.Code, res.Body.String())
	}

	// The same model again, renamed, with no weights in the entry at all.
	renamed := map[string]any{
		"provider": "Upstream", "model_id": "vendor/priced", "display_name": "Renamed", "enabled": true,
	}
	fresh := map[string]any{"provider": "Upstream", "model_id": "vendor/fresh", "display_name": "Fresh", "enabled": true}
	if res := in.do(http.MethodPost, "/api/admin/models/import", map[string]any{"models": []any{renamed, fresh}}, admin); res.Code != http.StatusOK {
		t.Fatalf("second import: %d %s", res.Code, res.Body.String())
	}

	listing := decode[map[string]any](t, in.do(http.MethodGet, "/api/admin/models", nil, admin))
	rows, _ := listing["models"].([]any)
	byName := map[string]map[string]any{}
	for _, entry := range rows {
		if row, ok := entry.(map[string]any); ok {
			byName[row["display_name"].(string)] = row
		}
	}
	kept := byName["Renamed"]
	if kept == nil {
		t.Fatalf("the renamed model is not listed: %v", byName)
	}
	if kept["input_token_weight"] != 3.0 || kept["output_token_weight"] != 2.0 ||
		kept["reasoning_token_weight"] != 1.0 || kept["request_weight"] != 0.5 {
		t.Errorf("an entry without weights changed them: %v", kept)
	}
	if got := byName["Fresh"]; got == nil || got["input_token_weight"] != 1.0 || got["output_token_weight"] != 1.0 || got["reasoning_token_weight"] != 1.0 {
		t.Errorf("a new model from an entry without weights = %v, want 1x token weights", got)
	}
}
