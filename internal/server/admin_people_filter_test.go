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
