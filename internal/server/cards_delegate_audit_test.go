package server

import (
	"net/http"
	"testing"
	"time"
)

// A delegate holding only the users grant may move or revoke the unused cards
// of a plain account, but not those of another administrator: CanManageAdmin
// refuses that for every other action that reaches an administrator.
func TestAUsersDelegateCannotMoveOrRevokeAnAdministratorsCards(t *testing.T) {
	in := newInstance(t)
	founder := in.register("cards-founder", "a-good-password")
	delegate := in.register("cards-delegate", "a-good-password")
	admin := in.register("cards-admin", "a-good-password")
	reader := in.register("cards-reader", "a-good-password")

	setRole := func(target *session, role string, permissions []string) {
		t.Helper()
		res := in.do(http.MethodPatch, "/api/admin/users/"+target.userID,
			map[string]any{"role": role, "admin_permissions": permissions}, founder)
		if res.Code != http.StatusOK {
			t.Fatalf("set %s to %s: %d %s", target.userID, role, res.Code, res.Body.String())
		}
	}
	setRole(delegate, "admin", []string{"users"})
	setRole(admin, "admin", []string{"groups"})

	later := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second).UnixMilli()
	grant := func(target *session) string {
		t.Helper()
		res := in.do(http.MethodPost, "/api/admin/users/"+target.userID+"/cards",
			map[string]any{"cards": 1, "expires_at": later}, founder)
		if res.Code != http.StatusCreated {
			t.Fatalf("grant card to %s: %d %s", target.userID, res.Code, res.Body.String())
		}
		return decode[struct {
			Cards []struct {
				ID string `json:"id"`
			} `json:"cards"`
		}](t, res).Cards[0].ID
	}
	adminCard := grant(admin)
	readerCard := grant(reader)

	moveTo := map[string]any{"expires_at": later + 24*time.Hour.Milliseconds()}
	if res := in.do(http.MethodPatch, "/api/admin/users/"+admin.userID+"/cards", moveTo, delegate); res.Code != http.StatusForbidden {
		t.Errorf("a users delegate moved an administrator's cards: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodDelete, "/api/admin/users/"+admin.userID+"/cards/"+adminCard, nil, delegate); res.Code != http.StatusForbidden {
		t.Errorf("a users delegate revoked an administrator's card: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPatch, "/api/admin/users/"+reader.userID+"/cards", moveTo, delegate); res.Code != http.StatusOK {
		t.Errorf("a users delegate could not move a plain account's card: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodDelete, "/api/admin/users/"+reader.userID+"/cards/"+readerCard, nil, delegate); res.Code == http.StatusForbidden {
		t.Errorf("a users delegate was refused a plain account's card: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPatch, "/api/admin/users/"+admin.userID+"/cards", moveTo, founder); res.Code != http.StatusOK {
		t.Errorf("the super administrator could not move the administrator's cards: %d %s", res.Code, res.Body.String())
	}
}
