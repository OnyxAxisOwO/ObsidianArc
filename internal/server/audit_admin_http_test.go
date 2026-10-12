package server

import (
	"net/http"
	"testing"
)

// grantAdmin makes account an administrator holding exactly these grants.
// Every case below starts from a founder who can do it, so the test reads as
// what the delegate is allowed to reach and nothing else.
func grantAdmin(t *testing.T, in *instance, founder, account *session, permissions ...string) {
	t.Helper()
	res := in.do(http.MethodPatch, "/api/admin/users/"+account.userID,
		map[string]any{"role": "admin", "admin_permissions": permissions}, founder)
	if res.Code != http.StatusOK {
		t.Fatalf("grant %v: %d %s", permissions, res.Code, res.Body.String())
	}
}

// A users-only delegate reads an account's usage, quota and cards through
// the detail panel. The sibling reads of the same account (keys, sessions,
// conversations) already refuse a super administrator's account to that
// delegate; the panel must answer the same way.
func TestUserDetailPanelFollowsTheSiblingReadRule(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	member := in.register("member", "a-good-password")
	grantAdmin(t, in, founder, operator, "users")

	if res := in.do(http.MethodGet, "/api/admin/users/"+founder.userID, nil, operator); res.Code != http.StatusForbidden {
		t.Fatalf("users-only delegate read a super administrator's panel: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodGet, "/api/admin/users/"+founder.userID+"/keys", nil, operator); res.Code != http.StatusForbidden {
		t.Fatalf("sibling read changed: %d", res.Code)
	}
	if res := in.do(http.MethodGet, "/api/admin/users/"+member.userID, nil, operator); res.Code != http.StatusOK {
		t.Fatalf("ordinary account panel: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodGet, "/api/admin/users/"+founder.userID, nil, founder); res.Code != http.StatusOK {
		t.Fatalf("super administrator's own panel: %d %s", res.Code, res.Body.String())
	}
}
