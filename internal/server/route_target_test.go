package server

import (
	"net/http"
	"strings"
	"testing"
)

// The admin form sends the route target as typed. A target that names no
// model is a mistake in the form, so it must read as one, not as the model
// being saved being unavailable: the handler's own pre-check gets there first.
func TestRoutingToAMissingModelIsAFormError(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	provider := decode[map[string]any](t, in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Example host", "kind": "openai", "base_url": "https://api.example.com/v1", "api_key": "sk-test-key",
	}, admin))["provider"].(map[string]any)["id"].(string)

	res := in.do(http.MethodPost, "/api/admin/models", map[string]any{
		"provider_id": provider, "model_id": "front", "display_name": "Front", "enabled": true,
		"route_to_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
	}, admin)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "Route target does not exist.") {
		t.Fatalf("route to a missing model: %d %s, want a 400 naming the target", res.Code, res.Body.String())
	}
}
