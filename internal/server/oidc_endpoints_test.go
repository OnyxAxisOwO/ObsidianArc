package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// An OpenID Connect address is refused when it is saved, so the operator is
// told which key at the form, and the import that carries one drops it and
// names it. Nothing plaintext is stored for a later sign-in to find.
func TestOIDCAddressesMustBeHTTPSWhenSaved(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	for name, change := range map[string]map[string]string{
		"an issuer over plain http":           {"oauth.oidc_issuer": "http://idp.example.com"},
		"an authorization endpoint over http": {"oauth.oidc_auth_url": "http://idp.example.com/authorize"},
		"a token endpoint over http":          {"oauth.oidc_token_url": "http://idp.example.com/token"},
		"a userinfo endpoint over http":       {"oauth.oidc_userinfo_url": "http://idp.example.com/userinfo"},
	} {
		if response := in.do(http.MethodPut, "/api/admin/settings", change, founder); response.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want a refusal", name, response.Code)
		}
	}
	if read := in.do(http.MethodGet, "/api/admin/settings", nil, founder); strings.Contains(read.Body.String(), "http://idp.example.com") {
		t.Error("a refused save left a plaintext address in the settings")
	}

	saved := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		"oauth.oidc_issuer":       "https://idp.example.com",
		"oauth.oidc_token_url":    "http://127.0.0.1:8080/token",
		"oauth.oidc_userinfo_url": "",
	}, founder)
	if saved.Code != http.StatusOK {
		t.Fatalf("an https issuer and a loopback token URL: %d %s", saved.Code, saved.Body.String())
	}

	imported := in.do(http.MethodPost, "/api/admin/settings/import", map[string]string{
		"oauth.oidc_token_url": "http://idp.example.com/token",
		"site.name":            "Imported",
	}, founder)
	if imported.Code != http.StatusOK {
		t.Fatalf("import: %d %s", imported.Code, imported.Body.String())
	}
	var result struct {
		Applied int      `json:"applied"`
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(imported.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode import result: %v", err)
	}
	if !slices.Contains(result.Skipped, "oauth.oidc_token_url") {
		t.Errorf("skipped = %v, want the plaintext token URL named", result.Skipped)
	}
	if result.Applied != 1 {
		t.Errorf("applied = %d, want only the site name", result.Applied)
	}
	if read := in.do(http.MethodGet, "/api/admin/settings", nil, founder); strings.Contains(read.Body.String(), "http://idp.example.com/token") {
		t.Error("an import stored a plaintext token URL")
	}
}

// The screen can only say which field to change if the refusal says which one.
// A bare bad_request left the operator with a failed save and no field to look
// at, so the setting travels with the code.
func TestOIDCRefusalNamesTheSetting(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		"oauth.oidc_userinfo_url": "http://idp.example.com/userinfo",
	}, founder)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want a refusal: %s", response.Code, response.Body.String())
	}
	var refusal struct {
		Error struct {
			Code    string `json:"code"`
			Setting string `json:"setting"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if refusal.Error.Code != "oidc_url_not_https" || refusal.Error.Setting != "oauth.oidc_userinfo_url" {
		t.Errorf("refusal = %+v, want oidc_url_not_https naming oauth.oidc_userinfo_url", refusal.Error)
	}
}

// An address already stored in plain http must not hold every other change on
// the security screen hostage: the save changes nothing about it, and the
// sign-in refuses it at use anyway. Typing a different plaintext address is
// still a new refusal, and so is importing one.
func TestAStoredPlaintextOIDCAddressOnlyBlocksANewOne(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	if err := in.server.settings.SetMany(context.Background(), map[string]string{
		settings.OAuthOIDCTokenURL: "http://idp.example.com/token",
	}); err != nil {
		t.Fatalf("store a plaintext token URL: %v", err)
	}

	unrelated := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		settings.OAuthOIDCTokenURL: "http://idp.example.com/token",
		settings.SiteName:          "Renamed",
	}, founder)
	if unrelated.Code != http.StatusOK {
		t.Fatalf("a save that keeps the stored address: %d %s", unrelated.Code, unrelated.Body.String())
	}
	if got := in.server.settings.Get(settings.SiteName); got != "Renamed" {
		t.Errorf("site name = %q, want the unrelated change saved", got)
	}

	changed := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		settings.OAuthOIDCTokenURL: "http://idp.example.com/other-token",
	}, founder)
	if changed.Code != http.StatusBadRequest {
		t.Errorf("a new plaintext token URL: %d, want a refusal", changed.Code)
	}

	imported := in.do(http.MethodPost, "/api/admin/settings/import", map[string]string{
		settings.OAuthOIDCTokenURL:    "http://idp.example.com/token",
		settings.OAuthOIDCUserInfoURL: "http://idp.example.com/userinfo",
	}, founder)
	if imported.Code != http.StatusOK {
		t.Fatalf("import: %d %s", imported.Code, imported.Body.String())
	}
	var result struct {
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(imported.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode import result: %v", err)
	}
	if slices.Contains(result.Skipped, settings.OAuthOIDCTokenURL) {
		t.Errorf("import skipped the address already stored, which changes nothing: %v", result.Skipped)
	}
	if !slices.Contains(result.Skipped, settings.OAuthOIDCUserInfoURL) {
		t.Errorf("skipped = %v, want the new plaintext userinfo URL named", result.Skipped)
	}
}
