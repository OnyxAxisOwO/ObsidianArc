package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The auth card layout position controls whether the login / registration card
// sits in the center (default) or docks to the left or right of the screen.
func TestAuthCardPositionSettingsAndSiteAPI(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	member := in.register("visitor", "another-password")

	readPosition := func() string {
		t.Helper()
		response := in.do(http.MethodGet, "/api/site", nil, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("GET /api/site: %d %s", response.Code, response.Body.String())
		}
		var payload struct {
			AuthCardPosition string `json:"auth_card_position"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.AuthCardPosition
	}

	// 1. Fresh instance defaults to "center".
	if got := readPosition(); got != settings.AuthCardPositionCenter {
		t.Fatalf("auth_card_position = %q, want %q", got, settings.AuthCardPositionCenter)
	}

	// 2. Admin can set it to "left".
	res := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{settings.SiteAuthCardPosition: "left"}, admin)
	if res.Code != http.StatusOK {
		t.Fatalf("set auth card position to left: %d %s", res.Code, res.Body.String())
	}
	if got := readPosition(); got != "left" {
		t.Fatalf("auth_card_position = %q, want left", got)
	}

	// 3. Admin can set it to "right".
	res = in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{settings.SiteAuthCardPosition: "right"}, admin)
	if res.Code != http.StatusOK {
		t.Fatalf("set auth card position to right: %d %s", res.Code, res.Body.String())
	}
	if got := readPosition(); got != "right" {
		t.Fatalf("auth_card_position = %q, want right", got)
	}

	// 4. Admin can set it back to "center".
	res = in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{settings.SiteAuthCardPosition: "center"}, admin)
	if res.Code != http.StatusOK {
		t.Fatalf("set auth card position to center: %d %s", res.Code, res.Body.String())
	}
	if got := readPosition(); got != "center" {
		t.Fatalf("auth_card_position = %q, want center", got)
	}

	// 5. Invalid values are refused.
	badRes := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{settings.SiteAuthCardPosition: "top"}, admin)
	if badRes.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for position 'top', got %d", badRes.Code)
	}

	// 6. Non-admin cannot modify the setting.
	forbiddenRes := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{settings.SiteAuthCardPosition: "left"}, member)
	if forbiddenRes.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for non-admin, got %d", forbiddenRes.Code)
	}

	// 7. Settings import keeps a valid auth card position.
	importRes := in.do(http.MethodPost, "/api/admin/settings/import", map[string]string{
		settings.SiteAuthCardPosition: "right",
	}, admin)
	if importRes.Code != http.StatusOK {
		t.Fatalf("import valid settings: %d %s", importRes.Code, importRes.Body.String())
	}
	if got := readPosition(); got != "right" {
		t.Fatalf("auth_card_position after import = %q, want right", got)
	}

	// 8. Settings import drops an invalid auth card position and lists it in skipped.
	importBadRes := in.do(http.MethodPost, "/api/admin/settings/import", map[string]string{
		settings.SiteAuthCardPosition: "bottom",
	}, admin)
	if importBadRes.Code != http.StatusOK {
		t.Fatalf("import bad settings: %d %s", importBadRes.Code, importBadRes.Body.String())
	}
	var importResult struct {
		Applied int      `json:"applied"`
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(importBadRes.Body.Bytes(), &importResult); err != nil {
		t.Fatal(err)
	}
	if len(importResult.Skipped) != 1 || importResult.Skipped[0] != settings.SiteAuthCardPosition {
		t.Fatalf("expected skipped [%s], got %v", settings.SiteAuthCardPosition, importResult.Skipped)
	}
	// Position remains unchanged at "right".
	if got := readPosition(); got != "right" {
		t.Fatalf("auth_card_position after skipped import = %q, want right", got)
	}
}
