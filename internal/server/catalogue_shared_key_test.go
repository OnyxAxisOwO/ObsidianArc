package server

import (
	"net/http"
	"testing"
)

// Two configurations of one upstream model are legal: the table allows them so
// an operator can run one model several ways. A file names a model by provider
// and upstream id, which cannot say which of the two it means, so the import
// reports the entry rather than writing it over whichever row came last.
func TestImportLeavesSeveralConfigurationsOfOneModelAlone(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	provider := decode[struct {
		Provider struct {
			ID string `json:"id"`
		} `json:"provider"`
	}](t, in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Upstream", "kind": "openai",
		"base_url": "https://api.example.com/v1", "api_key": "sk-test-key-0123",
	}, admin))

	for _, name := range []string{"Big A", "Big B"} {
		if res := in.do(http.MethodPost, "/api/admin/models", map[string]any{
			"provider_id": provider.Provider.ID, "model_id": "vendor/big", "display_name": name, "enabled": true,
		}, admin); res.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, res.Code, res.Body.String())
		}
	}

	result := decode[map[string]any](t, in.do(http.MethodPost, "/api/admin/models/import", map[string]any{
		"models": []any{map[string]any{
			"provider": "Upstream", "model_id": "vendor/big", "display_name": "From the file", "enabled": true,
		}},
	}, admin))
	if result["created"] != 0.0 || result["updated"] != 0.0 {
		t.Errorf("import touched a shared key: %+v", result)
	}
	if skipped, _ := result["skipped"].([]any); len(skipped) != 1 {
		t.Errorf("skipped %v, want the one entry reported", skipped)
	}

	listing := decode[map[string]any](t, in.do(http.MethodGet, "/api/admin/models", nil, admin))
	names := map[string]bool{}
	rows, _ := listing["models"].([]any)
	for _, entry := range rows {
		if row, ok := entry.(map[string]any); ok {
			names[row["display_name"].(string)] = true
		}
	}
	if !names["Big A"] || !names["Big B"] || names["From the file"] {
		t.Errorf("listed names %v, want both configurations unchanged", names)
	}
}
