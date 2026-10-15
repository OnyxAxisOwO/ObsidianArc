package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An image turn is charged credits for each picture and records no tokens, so
// the per-minute token window has nothing to hold back for the extra pictures.
// Scaling the reservation's tokens by the picture count would hold a call to
// four pictures' worth of a window that no image turn ever charges.
func TestAFourPictureImageCallIsNotRefusedByTheTokenWindow(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	member := in.register("member", "a-good-password")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var asked struct {
			N int `json:"n"`
		}
		_ = json.NewDecoder(r.Body).Decode(&asked)
		if asked.N < 1 {
			asked.N = 1
		}
		var data strings.Builder
		for i := 0; i < asked.N; i++ {
			if i > 0 {
				data.WriteString(",")
			}
			data.WriteString(`{"b64_json":"` + generatedPNGForServerTests + `"}`)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1,"data":[` + data.String() + `]}`))
	}))
	t.Cleanup(upstream.Close)

	provider := decode[map[string]any](t, in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Painter host", "kind": "openai", "base_url": upstream.URL, "api_key": "sk-test-key",
	}, admin))["provider"].(map[string]any)["id"].(string)
	created := in.do(http.MethodPost, "/api/admin/models", map[string]any{
		"provider_id": provider, "model_id": "painter", "display_name": "Painter", "enabled": true,
		"supports_image_gen": true, "request_weight": 1, "output_token_weight": 0,
	}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create the model: %d %s", created.Code, created.Body.String())
	}
	catalogueID := decode[map[string]any](t, created)["model"].(map[string]any)["id"].(string)

	// A per-minute token window that holds one picture's reservation but not
	// four. Nothing has been used yet, so only the reservation decides.
	if res := in.do(http.MethodPut, "/api/admin/quota/policies", map[string]any{
		"scope": "global", "tpm": 5000,
	}, admin); res.Code != http.StatusOK {
		t.Fatalf("save policy: %d %s", res.Code, res.Body.String())
	}

	res := in.do(http.MethodPost, "/api/images/generate", map[string]any{
		"model_id": catalogueID, "prompt": "a lighthouse", "n": 4,
	}, member)
	if res.Code != http.StatusOK {
		t.Fatalf("four pictures refused by a window that image turns never charge tokens to: %d %s", res.Code, res.Body.String())
	}
}
