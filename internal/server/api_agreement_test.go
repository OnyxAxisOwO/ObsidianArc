package server

import (
	"net/http"
	"strings"
	"testing"
)

// The keys screen and /v1 each decide whether an account may use the API, and
// they name a refusal differently on purpose. A rule changed in one and not in
// the other would let a key through that the screen refuses, and no single
// test would notice, so this checks that both give the same answer as the
// restriction and the switch change.
func TestTheKeysScreenAndTheAPIAgreeOnWhoMayUseIt(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	member := in.register("member", "another-password")
	in.enableAPI(founder)
	token := in.mintKey(member)

	setMember := func(values map[string]any) {
		t.Helper()
		if response := in.do(http.MethodPatch, "/api/admin/users/"+member.userID, values, founder); response.Code != http.StatusOK {
			t.Fatalf("update the member: %d %s", response.Code, response.Body.String())
		}
	}

	setMember(map[string]any{"api_restricted": true, "api_restriction_hours": 30})
	screen := in.do(http.MethodPost, "/api/keys", map[string]any{"name": "again"}, member)
	if screen.Code != http.StatusForbidden || !strings.Contains(screen.Body.String(), `"code":"api_restricted"`) {
		t.Fatalf("the keys screen under a restriction: %d %s", screen.Code, screen.Body.String())
	}
	held := in.doWithKey(http.MethodGet, "/v1/models", token)
	if held.Code != http.StatusForbidden || !strings.Contains(held.Body.String(), `"code":"api_restricted"`) {
		t.Fatalf("a key minted before the restriction: %d %s", held.Code, held.Body.String())
	}

	setMember(map[string]any{"api_restricted": false})
	if response := in.do(http.MethodPost, "/api/keys", map[string]any{"name": "restored"}, member); response.Code != http.StatusCreated {
		t.Fatalf("the keys screen after the restriction lifted: %d %s", response.Code, response.Body.String())
	}
	if code := in.doWithKey(http.MethodGet, "/v1/models", token).Code; code != http.StatusOK {
		t.Fatalf("the key after the restriction lifted: %d", code)
	}

	switched := map[string]string{"api.enabled": "false"}
	if response := in.do(http.MethodPut, "/api/admin/settings", switched, founder); response.Code != http.StatusOK {
		t.Fatalf("switch the API off: %d %s", response.Code, response.Body.String())
	}
	screen = in.do(http.MethodPost, "/api/keys", map[string]any{"name": "off"}, member)
	if screen.Code != http.StatusForbidden || !strings.Contains(screen.Body.String(), `"code":"api_disabled"`) {
		t.Fatalf("the keys screen with the API off: %d %s", screen.Code, screen.Body.String())
	}
	// /v1 answers 404 where the screen answers 403: a disabled API is not
	// something a stranger holding a key should be able to confirm exists.
	held = in.doWithKey(http.MethodGet, "/v1/models", token)
	if held.Code != http.StatusNotFound || !strings.Contains(held.Body.String(), `"code":"api_disabled"`) {
		t.Fatalf("a key with the API off: %d %s", held.Code, held.Body.String())
	}
}
