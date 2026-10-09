package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
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
