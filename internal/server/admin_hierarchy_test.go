package server

import (
	"net/http"
	"testing"
)

// A delegated users grant answers to the rule sessions already kept: it acts
// on ordinary accounts, not on a super administrator. Keys and conversations
// skipped it, so the grant could read a super administrator's chats and
// revoke their keys.
func TestADelegatedUsersGrantCannotReachASuperAdministrator(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	member := in.register("member", "a-good-password")
	if response := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"users"}}, founder); response.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", response.Code, response.Body.String())
	}

	for _, path := range []string{
		"/api/admin/users/" + founder.userID + "/keys",
		"/api/admin/users/" + founder.userID + "/conversations",
		"/api/admin/users/" + founder.userID + "/conversations/01ARZ3NDEKTSV4RRFFQ69G5FAV",
	} {
		if response := in.do(http.MethodGet, path, nil, operator); response.Code != http.StatusForbidden {
			t.Errorf("GET %s by a delegated grant = %d, want refused", path, response.Code)
		}
	}
	if response := in.do(http.MethodDelete,
		"/api/admin/users/"+founder.userID+"/keys/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil, operator); response.Code != http.StatusForbidden {
		t.Errorf("revoking a super administrator's key = %d, want refused", response.Code)
	}

	for _, path := range []string{
		"/api/admin/users/" + member.userID + "/keys",
		"/api/admin/users/" + member.userID + "/conversations",
	} {
		if response := in.do(http.MethodGet, path, nil, operator); response.Code != http.StatusOK {
			t.Errorf("GET %s for an ordinary account = %d %s", path, response.Code, response.Body.String())
		}
	}

	// Nor does anybody reset their own password from here, where only the
	// session is asked for.
	if response := in.do(http.MethodPost, "/api/admin/users/"+founder.userID+"/password",
		map[string]string{"new_password": "another-good-password"}, founder); response.Code != http.StatusBadRequest {
		t.Errorf("a super administrator resetting their own password = %d, want refused", response.Code)
	}
}
