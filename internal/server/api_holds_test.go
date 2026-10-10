package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

// The API's door to the two holds, through the assembled server. The compat
// package's own tests build its handlers by hand; this is where the predicates
// are wired to them, and a line missing from server.go would pass every test
// in that package.

// doWithKey is in.do for the API: a bearer credential and no session.
func (in *instance) doWithKey(method, path, token string) *httptest.ResponseRecorder {
	in.t.Helper()
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, request)
	return recorder
}

// enableAPI switches the instance API on and grants the default group access
// to it. The bootstrap group does not carry that grant, and the keys screen
// will not mint a key for a group that cannot use one.
func (in *instance) enableAPI(as *session) {
	in.t.Helper()
	if response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"api.enabled": "true"}, as); response.Code != http.StatusOK {
		in.t.Fatalf("enable the API: %d %s", response.Code, response.Body.String())
	}
	ctx := context.Background()
	groups := group.NewStore(in.db)
	defaults, err := groups.Default(ctx, nil)
	if err != nil {
		in.t.Fatal(err)
	}
	granted := true
	if _, err := groups.Update(ctx, nil, defaults.ID, group.Update{APIAccess: &granted}); err != nil {
		in.t.Fatal(err)
	}
}

// mintKey issues an API key for a signed-in account, as the keys screen does.
func (in *instance) mintKey(as *session) string {
	in.t.Helper()
	response := in.do(http.MethodPost, "/api/keys", map[string]any{"name": "tool"}, as)
	if response.Code != http.StatusCreated {
		in.t.Fatalf("mint key: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.Token == "" {
		in.t.Fatalf("mint key returned no token: %s", response.Body.String())
	}
	return payload.Token
}

// The key is minted before the policy exists, which is the case the session
// gates cannot see: it is the one a rollout leaves behind.
func TestTheTwoStepHoldReachesAKeyMintedBeforeThePolicy(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	member := in.register("member", "another-password")

	in.enableAPI(founder)
	token := in.mintKey(member)
	if code := in.doWithKey(http.MethodGet, "/v1/models", token).Code; code != http.StatusOK {
		t.Fatalf("the key before any policy: %d", code)
	}

	// The founder enrols first, or the policy holds them out of the settings
	// screen that would switch it off again.
	in.enrol(founder)
	policy := map[string]string{"security.two_factor_policy": "everyone"}
	if response := in.do(http.MethodPut, "/api/admin/settings", policy, founder); response.Code != http.StatusOK {
		t.Fatalf("policy: %d %s", response.Code, response.Body.String())
	}

	held := in.doWithKey(http.MethodGet, "/v1/models", token)
	if held.Code != http.StatusForbidden || !strings.Contains(held.Body.String(), "two_factor_enrolment_required") {
		t.Fatalf("an unenrolled account's key under the everyone policy: %d %s", held.Code, held.Body.String())
	}

	in.enrol(member)
	if code := in.doWithKey(http.MethodGet, "/v1/models", token).Code; code != http.StatusOK {
		t.Fatalf("the key after enrolling: %d", code)
	}
}

func TestTheOIDCBindingHoldReachesAKeyMintedBeforeThePolicy(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	member := in.register("member", "another-password")

	in.enableAPI(founder)
	stub := stubOIDCProvider(t)
	configure := map[string]string{
		"oauth.oidc_enabled":       "true",
		"oauth.oidc_client_id":     "a-client-id",
		"oauth.oidc_client_secret": "a-client-secret",
		"oauth.oidc_auth_url":      stub.URL + "/authorize",
		"oauth.oidc_token_url":     stub.URL + "/token",
		"oauth.oidc_userinfo_url":  stub.URL + "/userinfo",
	}
	if response := in.do(http.MethodPut, "/api/admin/settings", configure, founder); response.Code != http.StatusOK {
		t.Fatalf("configure: %d %s", response.Code, response.Body.String())
	}
	token := in.mintKey(member)
	if code := in.doWithKey(http.MethodGet, "/v1/models", token).Code; code != http.StatusOK {
		t.Fatalf("the key before any policy: %d", code)
	}

	policy := map[string]string{"oauth.oidc_require_for_all": "true"}
	if response := in.do(http.MethodPut, "/api/admin/settings", policy, founder); response.Code != http.StatusOK {
		t.Fatalf("policy: %d %s", response.Code, response.Body.String())
	}

	held := in.doWithKey(http.MethodGet, "/v1/models", token)
	if held.Code != http.StatusForbidden || !strings.Contains(held.Body.String(), "oidc_binding_required") {
		t.Fatalf("an unbound account's key under require-for-all: %d %s", held.Code, held.Body.String())
	}
}
