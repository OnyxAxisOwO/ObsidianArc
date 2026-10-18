package server

import (
	"encoding/json"
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

// The held-cards read answers to the account panel's rule, not the users grant
// alone: an administrator's cards are their own allowance.
func TestReadingHeldCardsAnswersToTheAccountPanelsRule(t *testing.T) {
	in := newInstance(t)
	founder := in.register("held-founder", "a-good-password")
	delegate := in.register("held-delegate", "a-good-password")
	admin := in.register("held-admin", "a-good-password")
	reader := in.register("held-reader", "a-good-password")

	for _, target := range []struct {
		who         *session
		permissions []string
	}{{delegate, []string{"users"}}, {admin, []string{"groups"}}} {
		res := in.do(http.MethodPatch, "/api/admin/users/"+target.who.userID,
			map[string]any{"role": "admin", "admin_permissions": target.permissions}, founder)
		if res.Code != http.StatusOK {
			t.Fatalf("promote %s: %d %s", target.who.userID, res.Code, res.Body.String())
		}
	}

	later := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second).UnixMilli()
	for _, target := range []*session{admin, reader} {
		res := in.do(http.MethodPost, "/api/admin/users/"+target.userID+"/cards",
			map[string]any{"cards": 2, "expires_at": later}, founder)
		if res.Code != http.StatusCreated {
			t.Fatalf("grant cards to %s: %d %s", target.userID, res.Code, res.Body.String())
		}
	}

	if res := in.do(http.MethodGet, "/api/admin/users/"+admin.userID+"/cards", nil, delegate); res.Code != http.StatusForbidden {
		t.Errorf("a users delegate read an administrator's cards: %d %s", res.Code, res.Body.String())
	}

	res := in.do(http.MethodGet, "/api/admin/users/"+reader.userID+"/cards", nil, delegate)
	if res.Code != http.StatusOK {
		t.Fatalf("a users delegate could not read a plain account's cards: %d %s", res.Code, res.Body.String())
	}
	held := decode[struct {
		Cards json.RawMessage `json:"cards"`
	}](t, res).Cards
	panel := in.do(http.MethodGet, "/api/admin/users/"+reader.userID, nil, founder)
	if panel.Code != http.StatusOK {
		t.Fatalf("account panel: %d %s", panel.Code, panel.Body.String())
	}
	want := decode[struct {
		Cards json.RawMessage `json:"cards"`
	}](t, panel).Cards
	if string(held) != string(want) || string(held) == "null" {
		t.Errorf("held cards %s, want the panel's %s", held, want)
	}
}
