package server

import (
	"net/http"
	"testing"
)

// Where an issuer sends people and what it is trusted to vouch for decide
// whose word an account is opened on. A delegated security grant could set
// them, point them at an issuer of its own, and have it vouch for anybody's
// address; they are a super administrator's alone now. The rest of the
// security page stays delegable.
func TestOnlyASuperAdministratorPointsTheSignInProviderAnywhere(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	if response := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"security"}}, founder); response.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", response.Code, response.Body.String())
	}

	for _, key := range []string{
		"oauth.oidc_issuer", "oauth.oidc_auth_url", "oauth.oidc_token_url", "oauth.oidc_userinfo_url",
		"oauth.oidc_client_id", "oauth.oidc_client_secret", "oauth.oidc_trust_email", "oauth.link_by_email",
	} {
		response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{key: "https://evil.example"}, operator)
		if response.Code != http.StatusForbidden {
			t.Errorf("a delegated security grant set %s: %d %s", key, response.Code, response.Body.String())
		}
	}

	if response := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"oauth.oidc_enabled": "false", "turnstile.on_login": "false"}, operator); response.Code != http.StatusOK {
		t.Errorf("the rest of the security page was refused: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"oauth.oidc_issuer": "https://auth.example.com"}, founder); response.Code != http.StatusOK {
		t.Errorf("the super administrator was refused: %d %s", response.Code, response.Body.String())
	}
}
