package server

import (
	"net/http"
	"testing"
	"time"
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

// Moving or withdrawing an administrator's unused cards is taking something
// away from that administrator. A users-only delegate may not do it to a
// peer administrator it could not manage; the other routes that reach an
// administrator already refuse it, and these two were checked only against
// super administrators.
func TestDelegateCannotMoveOrRevokeAPeersCards(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	peer := in.register("peer", "a-good-password")
	member := in.register("member", "a-good-password")
	grantAdmin(t, in, founder, operator, "users")
	grantAdmin(t, in, founder, peer, "usage")

	granted := in.do(http.MethodPost, "/api/admin/users/"+peer.userID+"/cards",
		map[string]any{"cards": 1, "card_days": 30}, founder)
	if granted.Code != http.StatusCreated {
		t.Fatalf("grant: %d %s", granted.Code, granted.Body.String())
	}
	payload := decode[struct {
		Cards []struct {
			ID string `json:"id"`
		} `json:"cards"`
	}](t, granted)
	if len(payload.Cards) != 1 {
		t.Fatalf("granted %d cards", len(payload.Cards))
	}
	cardID := payload.Cards[0].ID
	later := map[string]any{"expires_at": time.Now().Add(90 * 24 * time.Hour).UnixMilli(), "card_ids": []string{cardID}}

	if res := in.do(http.MethodPatch, "/api/admin/users/"+peer.userID+"/cards", later, operator); res.Code != http.StatusForbidden {
		t.Fatalf("users-only delegate rescheduled a peer's card: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodDelete, "/api/admin/users/"+peer.userID+"/cards/"+cardID, nil, operator); res.Code != http.StatusForbidden {
		t.Fatalf("users-only delegate revoked a peer's card: %d %s", res.Code, res.Body.String())
	}

	// Ordinary accounts keep their cards in the delegate's hands.
	if res := in.do(http.MethodPost, "/api/admin/users/"+member.userID+"/cards", map[string]any{"cards": 1, "card_days": 30}, operator); res.Code != http.StatusCreated {
		t.Fatalf("delegate granted a member: %d %s", res.Code, res.Body.String())
	}
	// The founder, a super administrator, still reaches the peer's card.
	if res := in.do(http.MethodPatch, "/api/admin/users/"+peer.userID+"/cards", later, founder); res.Code != http.StatusOK {
		t.Fatalf("super administrator reschedule: %d %s", res.Code, res.Body.String())
	}
}
