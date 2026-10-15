package server

import (
	"net/http"
	"testing"
)

// The limits list answers each account-scoped limit with whether it names an
// administrator the caller may manage. The administrators are read once for the
// whole list, so the answer must be the same for every row: a users-only
// delegate sees a limit on an ordinary account and not on a peer it cannot manage.
func TestPolicyListHidesLimitsOnAdministratorsTheActorCannotManage(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	peer := in.register("peer", "a-good-password")
	member := in.register("member", "a-good-password")
	grantAdmin(t, in, founder, operator, "users")
	grantAdmin(t, in, founder, peer, "usage")

	for _, scopeID := range []string{member.userID, peer.userID} {
		body := map[string]any{
			"scope": "user", "scope_id": scopeID,
			"windows": map[string]any{"5h": map[string]any{"enabled": true, "tokens": 1000}},
		}
		if res := in.do(http.MethodPut, "/api/admin/quota/policies", body, founder); res.Code != http.StatusOK {
			t.Fatalf("limit on %s: %d %s", scopeID, res.Code, res.Body.String())
		}
	}

	listed := func(as *session) map[string]bool {
		t.Helper()
		res := in.do(http.MethodGet, "/api/admin/quota/policies", nil, as)
		if res.Code != http.StatusOK {
			t.Fatalf("list policies: %d %s", res.Code, res.Body.String())
		}
		out := map[string]bool{}
		for _, p := range decode[struct {
			Policies []struct {
				ScopeID string `json:"scope_id"`
			} `json:"policies"`
		}](t, res).Policies {
			out[p.ScopeID] = true
		}
		return out
	}

	if seen := listed(operator); !seen[member.userID] || seen[peer.userID] {
		t.Errorf("users-only delegate sees member=%v peer=%v, want member yes and peer no", seen[member.userID], seen[peer.userID])
	}
	if seen := listed(founder); !seen[member.userID] || !seen[peer.userID] {
		t.Errorf("super administrator sees member=%v peer=%v, want both", seen[member.userID], seen[peer.userID])
	}
}
