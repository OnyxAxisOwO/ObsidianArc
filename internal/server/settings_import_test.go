package server

import (
	"net/http"
	"testing"
)

// A settings export names the models it points at by ULID, and a ULID from
// another instance means nothing here. The sign-up reviewer's model is one of
// them, and it was the one not checked: carried across, every review resolved
// against a model this instance does not have, and the review fell back to the
// selected mode's failure decision — normal mode restricting, strict mode
// refusing everybody — with nothing in the response to say why.
func TestImportingSettingsDropsAModelIdThatIsNotHere(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	response := in.do(http.MethodPost, "/api/admin/settings/import", map[string]string{
		"security.signup_review_model": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"site.name":                    "Carried Over",
	}, admin)
	if response.Code != http.StatusOK {
		t.Fatalf("import: %d %s", response.Code, response.Body.String())
	}

	result := decode[map[string]any](t, response)
	skipped, _ := result["skipped"].([]any)
	if len(skipped) != 1 || skipped[0] != "security.signup_review_model" {
		t.Errorf("skipped = %v, want just security.signup_review_model", skipped)
	}

	settings := decode[map[string]any](t, in.do(http.MethodGet, "/api/admin/settings", nil, admin))
	values, _ := settings["settings"].(map[string]any)
	if values["security.signup_review_model"] != "" {
		t.Errorf("a model id that means nothing here was kept: %v",
			values["security.signup_review_model"])
	}
	// The rest of the same document still lands. One dangling identifier must
	// not cost the operator the export they are trying to move.
	if values["site.name"] != "Carried Over" {
		t.Errorf("site.name = %v, want the imported value", values["site.name"])
	}
}

// The other half of the same rule: an id that does resolve is kept. The
// clearing above is about the model being absent, not about the setting being
// refused, and a test that only checked the absent case could not tell those
// apart.
func TestImportingSettingsKeepsAModelIdThatIsHere(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	created := in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Upstream", "kind": "openai",
		"base_url": "https://api.example.com/v1", "api_key": "sk-test-key-0123",
	}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create provider: %d %s", created.Code, created.Body.String())
	}
	imported := in.do(http.MethodPost, "/api/admin/models/import", map[string]any{
		"models": []any{map[string]any{
			"provider": "Upstream", "model_id": "vendor/reviewer",
			"display_name": "Reviewer", "enabled": true,
		}},
	}, admin)
	if imported.Code != http.StatusOK {
		t.Fatalf("import model: %d %s", imported.Code, imported.Body.String())
	}

	listing := decode[map[string]any](t, in.do(http.MethodGet, "/api/admin/models", nil, admin))
	rows, _ := listing["models"].([]any)
	if len(rows) != 1 {
		t.Fatalf("listed %d models, want 1", len(rows))
	}
	row, _ := rows[0].(map[string]any)
	modelID, _ := row["id"].(string)
	if modelID == "" {
		t.Fatalf("the model arrived without an id: %+v", row)
	}

	response := in.do(http.MethodPost, "/api/admin/settings/import", map[string]string{
		"security.signup_review_model": modelID,
	}, admin)
	if response.Code != http.StatusOK {
		t.Fatalf("import settings: %d %s", response.Code, response.Body.String())
	}
	if result := decode[map[string]any](t, response); len(result["skipped"].([]any)) != 0 {
		t.Errorf("a model that is here was skipped: %v", result["skipped"])
	}

	settings := decode[map[string]any](t, in.do(http.MethodGet, "/api/admin/settings", nil, admin))
	values, _ := settings["settings"].(map[string]any)
	if values["security.signup_review_model"] != modelID {
		t.Errorf("stored model = %v, want %v", values["security.signup_review_model"], modelID)
	}
}

// An export shows a secret as the mask, so importing the same file back
// carries the mask. It used to be written over the real secret.
func TestImportingTheMaskKeepsTheSecret(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	if response := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"turnstile.secret_key": "the-real-secret"}, admin); response.Code != http.StatusOK {
		t.Fatalf("set: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodPost, "/api/admin/settings/import",
		map[string]string{"turnstile.secret_key": "••••••••", "site.name": "Moved"}, admin); response.Code != http.StatusOK {
		t.Fatalf("import: %d %s", response.Code, response.Body.String())
	}
	if got := in.server.settings.Get("turnstile.secret_key"); got != "the-real-secret" {
		t.Errorf("secret after importing the mask = %q", got)
	}
}
