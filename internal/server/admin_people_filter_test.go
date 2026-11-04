package server

import (
	"net/http"
	"testing"
)

// The console's username box is an exact match on the lowercased name. A
// prefix of another account's name is a different account, and typing the name
// in capitals must still find the one it belongs to.
func TestUserListFindsOneAccountByUsernameIgnoringCase(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	in.register("alice", "another-password")
	in.register("alicia", "another-password")

	found := decode[struct {
		Users []struct {
			Username string `json:"username"`
		} `json:"users"`
		Total int `json:"total"`
	}](t, in.do(http.MethodGet, "/api/admin/users?username=ALICE", nil, founder))
	if found.Total != 1 || len(found.Users) != 1 || found.Users[0].Username != "alice" {
		t.Fatalf("username=ALICE: total %d with %+v, want the one account alice", found.Total, found.Users)
	}
}

// A grant the model store would refuse is caught before the group is written,
// so a refused create leaves no group behind. The retry under the same name is
// the proof: a leftover group would turn it away as a duplicate.
func TestAGroupWithAnInvalidGrantIsNotLeftBehind(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	refused := in.do(http.MethodPost, "/api/admin/groups", map[string]any{
		"name":         "Retry Group",
		"model_grants": []map[string]string{{"model_id": "vendor/one", "access": "sometimes"}},
	}, founder)
	if refused.Code != http.StatusBadRequest {
		t.Fatalf("a group with an invalid access word: %d %s", refused.Code, refused.Body.String())
	}

	retry := in.do(http.MethodPost, "/api/admin/groups", map[string]any{"name": "Retry Group"}, founder)
	if retry.Code != http.StatusCreated {
		t.Fatalf("the retry after a refused grant: %d %s", retry.Code, retry.Body.String())
	}
}
