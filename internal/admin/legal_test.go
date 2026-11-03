package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

func TestLegalSettingsSaveAndReload(t *testing.T) {
	f := newSettingsFixture(t)
	values := map[string]string{
		settings.TermsTitle: "Site terms", settings.TermsBody: "# Terms\n\n**Read this**", settings.TermsEnabled: "true", settings.TermsRequired: "true",
		settings.PrivacyTitle: "Data policy", settings.PrivacyBody: "# Privacy", settings.PrivacyEnabled: "true", settings.PrivacyRequired: "false",
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/settings", strings.NewReader(string(raw)))
	req = req.WithContext(auth.WithUser(req.Context(), f.admin))
	rec := httptest.NewRecorder()
	if err := f.h.updateSettings(rec, req); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	reloaded := settings.New(f.db)
	if err := reloaded.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	for key, want := range values {
		if got := reloaded.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
		if permission := settingPermission(reloaded, key); permission != "security" {
			t.Errorf("%s belongs to %s, want security", key, permission)
		}
	}
	documents := reloaded.LegalDocuments()
	if len(documents) != 2 || !documents[0].Required || documents[1].Required {
		t.Fatalf("documents after restart: %+v", documents)
	}
}
