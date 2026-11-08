package server

import (
	"encoding/json"
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

	setMember(map[string]any{"api_restricted": true, "api_restriction_hours": 30, "api_restriction_reason": "请停止自动探测"})
	before := in.do(http.MethodGet, "/api/admin/users/"+member.userID, nil, founder)
	if before.Code != http.StatusOK {
		t.Fatalf("user detail: %d %s", before.Code, before.Body.String())
	}
	var initial map[string]any
	if err := json.Unmarshal(before.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	screen := in.do(http.MethodPost, "/api/keys", map[string]any{"name": "again"}, member)
	if screen.Code != http.StatusForbidden || !strings.Contains(screen.Body.String(), `"code":"api_restricted"`) {
		t.Fatalf("the keys screen under a restriction: %d %s", screen.Code, screen.Body.String())
	}
	held := in.doWithKey(http.MethodGet, "/v1/models", token)
	if held.Code != http.StatusForbidden || !strings.Contains(held.Body.String(), `"code":"api_restricted"`) {
		t.Fatalf("a key minted before the restriction: %d %s", held.Code, held.Body.String())
	}

	if !strings.Contains(held.Body.String(), "请停止自动探测") {
		t.Fatalf("restriction reason missing: %s", held.Body.String())
	}
	setMember(map[string]any{"api_restriction_reason": "请降低频率"})
	held = in.doWithKey(http.MethodGet, "/v1/models", token)
	if !strings.Contains(held.Body.String(), "请降低频率") {
		t.Fatalf("updated reason missing: %s", held.Body.String())
	}
	after := in.do(http.MethodGet, "/api/admin/users/"+member.userID, nil, founder)
	var updated map[string]any
	if err := json.Unmarshal(after.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	// The detail response includes more than the user, so compare the user objects.
	if initial["user"].(map[string]any)["api_restricted_until"] != updated["user"].(map[string]any)["api_restricted_until"] {
		t.Fatal("editing reason changed expiration")
	}
	setMember(map[string]any{"api_restricted": false})
	released := in.do(http.MethodGet, "/api/admin/users/"+member.userID, nil, founder)
	var releasedDetail map[string]any
	if err := json.Unmarshal(released.Body.Bytes(), &releasedDetail); err != nil {
		t.Fatal(err)
	}
	if releasedDetail["user"].(map[string]any)["api_restriction_reason"] != "" {
		t.Fatal("released restriction kept its reason")
	}
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
