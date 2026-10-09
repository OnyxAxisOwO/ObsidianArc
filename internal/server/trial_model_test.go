package server

import (
	"net/http"
	"strings"
	"testing"
)

// The trial is authorised as an administrator, because the visitor has no
// account to authorise as. With no model nominated for the front door it used
// to take the first enabled one, which put a visitor with no account in front
// of a model nobody had chosen to offer them.
func TestTheTrialDoesNotPickAModelNobodyNominated(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	provider := decode[map[string]any](t, in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Upstream", "kind": "openai", "base_url": "https://api.example.com/v1", "api_key": "sk-secret",
	}, admin))["provider"].(map[string]any)["id"].(string)
	created := in.do(http.MethodPost, "/api/admin/models", map[string]any{
		"provider_id": provider, "model_id": "hidden-model", "display_name": "Hidden", "enabled": true, "hidden": true,
	}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create the model: %d %s", created.Code, created.Body.String())
	}
	if res := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		"landing.mode": "chat", "landing.trial_enabled": "true",
	}, admin); res.Code != http.StatusOK {
		t.Fatalf("switch the trial on: %d %s", res.Code, res.Body.String())
	}

	res := in.do(http.MethodPost, "/api/trial/chat", map[string]any{
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}, nil)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("trial with no model nominated: %d %s, want 503", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "event:") {
		t.Fatalf("a stream was opened on a model nobody nominated: %s", res.Body.String())
	}
}
