package server

import (
	"net/http"
	"slices"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// Model IDs in a settings export may belong to another instance.
func TestImportingSettingsKeepsOnlyModelIDsThatResolve(t *testing.T) {
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

	for _, tc := range []struct{ id, stored string }{
		{"01ARZ3NDEKTSV4RRFFQ69G5FAV", ""},
		{modelID, modelID},
	} {
		response := in.do(http.MethodPost, "/api/admin/settings/import", map[string]string{
			settings.SignupReviewModel: tc.id, settings.SiteName: "Carried Over",
		}, admin)
		if response.Code != http.StatusOK {
			t.Fatalf("import settings: %d %s", response.Code, response.Body.String())
		}
		result := decode[struct {
			Skipped []string `json:"skipped"`
		}](t, response)
		want := []string{}
		if tc.stored == "" {
			want = []string{settings.SignupReviewModel}
		}
		if !slices.Equal(result.Skipped, want) {
			t.Fatalf("skipped = %v, want %v", result.Skipped, want)
		}
		if got := in.server.settings.Get(settings.SignupReviewModel); got != tc.stored {
			t.Errorf("stored model = %q, want %q", got, tc.stored)
		}
		if got := in.server.settings.Get(settings.SiteName); got != "Carried Over" {
			t.Errorf("the rest of the import did not land: site name = %q", got)
		}
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

// An administrator holds a grant for one section of the settings, and an
// export spans all of them. The key they may not change is named and left
// alone, and the key they may change still lands. Refusing the whole file for
// the first one was the old behaviour, and it left the grant nothing to apply.
func TestImportingSettingsOutsideAGrantSkipsOnlyThoseKeys(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	if response := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"settings"}}, founder); response.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", response.Code, response.Body.String())
	}
	before := in.server.settings.Get(settings.RegistrationEnabled)

	response := in.do(http.MethodPost, "/api/admin/settings/import", map[string]string{
		settings.RegistrationEnabled: "false",
		settings.SiteName:            "Carried Over",
	}, operator)
	if response.Code != http.StatusOK {
		t.Fatalf("import: %d %s", response.Code, response.Body.String())
	}
	result := decode[struct {
		Applied int      `json:"applied"`
		Skipped []string `json:"skipped"`
	}](t, response)
	if result.Applied != 1 || len(result.Skipped) != 1 || result.Skipped[0] != settings.RegistrationEnabled {
		t.Fatalf("applied %d, skipped %v; want one applied and only %s skipped",
			result.Applied, result.Skipped, settings.RegistrationEnabled)
	}
	if got := in.server.settings.Get(settings.RegistrationEnabled); got != before {
		t.Errorf("%s = %q after a key the grant does not cover, want %q", settings.RegistrationEnabled, got, before)
	}
	if got := in.server.settings.Get(settings.SiteName); got != "Carried Over" {
		t.Errorf("site.name = %q, want the imported value", got)
	}
}
