package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// setInviteSettings writes straight to the settings service rather than
// through PUT /api/admin/settings: these tests are about registration and
// the invite endpoints, not about the settings form, and going around it
// keeps each test to the one thing it is actually checking.
func (in *instance) setInviteSettings(t *testing.T, values map[string]string) {
	t.Helper()
	if err := in.server.settings.SetMany(context.Background(), values); err != nil {
		t.Fatalf("set invite settings: %v", err)
	}
}

type registerResponse struct {
	User struct {
		ID             string `json:"id"`
		GroupID        string `json:"group_id"`
		GroupExpiresAt int64  `json:"group_expires_at"`
	} `json:"user"`
}

type errorBody struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func errCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[errorBody](t, response).Error.Code
}

func TestRegistrationInviteOnlyRefusesWithoutOrWithABadCode(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	in.setInviteSettings(t, map[string]string{
		settings.RegistrationEnabled: "true", settings.InvitesRequired: "true",
	})

	res := in.do(http.MethodPost, "/api/auth/register",
		map[string]string{"username": "no-code", "password": "a-good-password"}, nil)
	if res.Code != http.StatusBadRequest || errCode(t, res) != "invite_required" {
		t.Fatalf("register with no code: %d %s", res.Code, res.Body.String())
	}

	res = in.do(http.MethodPost, "/api/auth/register",
		map[string]string{"username": "bad-code", "password": "a-good-password", "invite_code": "NOSUCHCODE"}, nil)
	if res.Code != http.StatusBadRequest || errCode(t, res) != "invite_invalid" {
		t.Fatalf("register with a bad code: %d %s", res.Code, res.Body.String())
	}

	created := in.do(http.MethodPost, "/api/admin/invites",
		map[string]any{"count": 1, "max_uses": 1}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create invite: %d %s", created.Code, created.Body.String())
	}
	code := decode[struct {
		Codes []struct{ Code string } `json:"codes"`
	}](t, created).Codes[0].Code

	res = in.do(http.MethodPost, "/api/auth/register",
		map[string]string{"username": "good-code", "password": "a-good-password", "invite_code": code}, nil)
	if res.Code != http.StatusCreated {
		t.Fatalf("register with a good code: %d %s", res.Code, res.Body.String())
	}

	// The code had one use; a second registration through it is refused the
	// same way an unknown one is.
	res = in.do(http.MethodPost, "/api/auth/register",
		map[string]string{"username": "second-try", "password": "a-good-password", "invite_code": code}, nil)
	if res.Code != http.StatusBadRequest || errCode(t, res) != "invite_invalid" {
		t.Fatalf("register through an exhausted code: %d %s", res.Code, res.Body.String())
	}
}

func TestRegistrationClosedRefusesEvenWithACode(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	created := in.do(http.MethodPost, "/api/admin/invites", map[string]any{"count": 1}, admin)
	code := decode[struct {
		Codes []struct{ Code string } `json:"codes"`
	}](t, created).Codes[0].Code

	in.setInviteSettings(t, map[string]string{settings.RegistrationEnabled: "false"})

	res := in.do(http.MethodPost, "/api/auth/register",
		map[string]string{"username": "closed-mode", "password": "a-good-password", "invite_code": code}, nil)
	if res.Code != http.StatusForbidden {
		t.Fatalf("register on a closed instance with a code: %d %s", res.Code, res.Body.String())
	}
}

func TestRegistrationAppliesAnAdminCodesGroupAndFixedDays(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	in.setInviteSettings(t, map[string]string{settings.RegistrationEnabled: "true"})

	groupRes := in.do(http.MethodPost, "/api/admin/groups", map[string]any{"name": "Partner Trial"}, admin)
	if groupRes.Code != http.StatusCreated {
		t.Fatalf("create group: %d %s", groupRes.Code, groupRes.Body.String())
	}
	groupID := decode[struct {
		Group struct{ ID string } `json:"group"`
	}](t, groupRes).Group.ID

	created := in.do(http.MethodPost, "/api/admin/invites",
		map[string]any{"count": 1, "group_id": groupID, "group_days": 5}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create invite: %d %s", created.Code, created.Body.String())
	}
	code := decode[struct {
		Codes []struct{ Code string } `json:"codes"`
	}](t, created).Codes[0].Code

	res := in.do(http.MethodPost, "/api/auth/register",
		map[string]string{"username": "trial-member", "password": "a-good-password", "invite_code": code}, nil)
	if res.Code != http.StatusCreated {
		t.Fatalf("register with the group code: %d %s", res.Code, res.Body.String())
	}
	account := decode[registerResponse](t, res).User
	if account.GroupID != groupID {
		t.Fatalf("group_id = %q, want %q", account.GroupID, groupID)
	}
	if account.GroupExpiresAt == 0 {
		t.Fatal("group_expires_at was not set for a 5-day trial")
	}
}

func TestRegistrationCustomPartnerCodeHasUnlimitedUses(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	in.setInviteSettings(t, map[string]string{settings.RegistrationEnabled: "true"})

	created := in.do(http.MethodPost, "/api/admin/invites",
		map[string]any{"count": 1, "code": "PARTNERX", "max_uses": 0}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create partner code: %d %s", created.Code, created.Body.String())
	}

	for i, username := range []string{"aff-one", "aff-two", "aff-three"} {
		res := in.do(http.MethodPost, "/api/auth/register",
			map[string]string{"username": username, "password": "a-good-password", "invite_code": "partner-x"}, nil)
		if res.Code != http.StatusCreated {
			t.Fatalf("register %d through the partner code: %d %s", i, res.Code, res.Body.String())
		}
	}

	// A second batch cannot claim the same text.
	taken := in.do(http.MethodPost, "/api/admin/invites",
		map[string]any{"count": 1, "code": "PARTNERX"}, admin)
	if taken.Code != http.StatusConflict || errCode(t, taken) != "invite_code_taken" {
		t.Fatalf("re-create the same partner code: %d %s", taken.Code, taken.Body.String())
	}
}

func TestInviteAdminEndpoints(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	created := in.do(http.MethodPost, "/api/admin/invites", map[string]any{"count": 3}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create batch: %d %s", created.Code, created.Body.String())
	}
	codes := decode[struct {
		Codes []struct {
			ID     string
			Status string
		} `json:"codes"`
	}](t, created).Codes
	if len(codes) != 3 {
		t.Fatalf("batch size = %d, want 3", len(codes))
	}

	list := in.do(http.MethodGet, "/api/admin/invites?kind=admin&status=active", nil, admin)
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	if total := decode[struct {
		Total int `json:"total"`
	}](t, list).Total; total != 3 {
		t.Fatalf("listed total = %d, want 3", total)
	}

	revoked := in.do(http.MethodDelete, "/api/admin/invites/"+codes[0].ID, nil, admin)
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", revoked.Code, revoked.Body.String())
	}
	if status := decode[struct {
		Code struct{ Status string } `json:"code"`
	}](t, revoked).Code.Status; status != "revoked" {
		t.Fatalf("revoked status = %q, want revoked", status)
	}
	// Idempotent.
	again := in.do(http.MethodDelete, "/api/admin/invites/"+codes[0].ID, nil, admin)
	if again.Code != http.StatusOK {
		t.Fatalf("revoke again: %d %s", again.Code, again.Body.String())
	}

	stats := in.do(http.MethodGet, "/api/admin/invites/stats", nil, admin)
	if stats.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", stats.Code, stats.Body.String())
	}
	if active := decode[struct {
		Active int `json:"active"`
	}](t, stats).Active; active != 2 {
		t.Fatalf("active = %d, want 2", active)
	}

	uses := in.do(http.MethodGet, "/api/admin/invites/"+codes[1].ID+"/uses", nil, admin)
	if uses.Code != http.StatusOK {
		t.Fatalf("uses: %d %s", uses.Code, uses.Body.String())
	}

	missing := in.do(http.MethodDelete, "/api/admin/invites/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil, admin)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("revoke a code that does not exist: %d %s", missing.Code, missing.Body.String())
	}
}

func TestProfileInvitesRoundTrip(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	off := in.do(http.MethodGet, "/api/profile/invites", nil, admin)
	if off.Code != http.StatusOK {
		t.Fatalf("profile invites (off): %d %s", off.Code, off.Body.String())
	}
	if enabled := decode[struct {
		Enabled bool `json:"enabled"`
	}](t, off).Enabled; enabled {
		t.Fatal("personal codes read enabled before the instance switched them on")
	}
	regenOff := in.do(http.MethodPost, "/api/profile/invites/regenerate", nil, admin)
	if regenOff.Code != http.StatusForbidden || errCode(t, regenOff) != "invites_disabled" {
		t.Fatalf("regenerate while off: %d %s", regenOff.Code, regenOff.Body.String())
	}

	in.setInviteSettings(t, map[string]string{
		settings.RegistrationEnabled: "true", settings.InvitesUserEnabled: "true",
		settings.InvitesRewardCards: "1", settings.InvitesUserLimit: "0",
	})

	on := in.do(http.MethodGet, "/api/profile/invites", nil, admin)
	first := decode[struct {
		Enabled bool   `json:"enabled"`
		Code    string `json:"code"`
	}](t, on)
	if !first.Enabled || first.Code == "" {
		t.Fatalf("profile invites (on): enabled=%v code=%q", first.Enabled, first.Code)
	}

	regen := in.do(http.MethodPost, "/api/profile/invites/regenerate", nil, admin)
	if regen.Code != http.StatusOK {
		t.Fatalf("regenerate: %d %s", regen.Code, regen.Body.String())
	}
	second := decode[struct {
		Code string `json:"code"`
	}](t, regen)
	if second.Code == first.Code {
		t.Fatal("regenerate returned the same code")
	}

	// The old code no longer works; the new one does, and the reward shows
	// up on both the inviter's card drawer and its own use list.
	stale := in.do(http.MethodPost, "/api/auth/register",
		map[string]string{"username": "stale-code", "password": "a-good-password", "invite_code": first.Code}, nil)
	if stale.Code != http.StatusBadRequest || errCode(t, stale) != "invite_invalid" {
		t.Fatalf("register through the regenerated-away code: %d %s", stale.Code, stale.Body.String())
	}

	joined := in.doFrom("203.0.113.66", "test-agent", http.MethodPost, "/api/auth/register",
		map[string]string{"username": "friend-of-admin", "password": "a-good-password", "invite_code": second.Code}, nil)
	if joined.Code != http.StatusCreated {
		t.Fatalf("register through the personal code: %d %s", joined.Code, joined.Body.String())
	}

	cards := in.do(http.MethodGet, "/api/usage/cards", nil, admin)
	if held := decode[struct {
		Cards []struct{ ID string } `json:"cards"`
	}](t, cards).Cards; len(held) != 1 {
		t.Fatalf("reward cards held = %d, want 1", len(held))
	}

	profile := in.do(http.MethodGet, "/api/profile/invites", nil, admin)
	final := decode[struct {
		Used     int `json:"used"`
		Counted  int `json:"counted"`
		Invitees []struct {
			Username string `json:"username"`
			Counted  bool   `json:"counted"`
		} `json:"invitees"`
	}](t, profile)
	if final.Used != 1 || final.Counted != 1 || len(final.Invitees) != 1 {
		t.Fatalf("profile after a join: used=%d counted=%d invitees=%d", final.Used, final.Counted, len(final.Invitees))
	}
	if !final.Invitees[0].Counted || final.Invitees[0].Username != "friend-of-admin" {
		t.Fatalf("invitee row = %+v, want counted friend-of-admin", final.Invitees[0])
	}

	notifications := in.do(http.MethodGet, "/api/notifications", nil, admin)
	body := decode[struct {
		Notifications []struct{ Kind string } `json:"notifications"`
	}](t, notifications)
	found := false
	for _, n := range body.Notifications {
		if n.Kind == "invite_joined" {
			found = true
		}
	}
	if !found {
		t.Fatal("no invite_joined notification reached the inviter")
	}
}

// TestClaimEndpoint covers POST /api/profile/invites/claim end to end: a
// signed-in account joining a partner code's group, a repeat claim refused
// as already-claimed, and a wrong code refused as invalid — the three
// responses a browser actually has to tell apart.
func TestClaimEndpoint(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	in.setInviteSettings(t, map[string]string{settings.RegistrationEnabled: "true"})
	member := in.register("claiming-member", "a-good-password")

	groupRes := in.do(http.MethodPost, "/api/admin/groups", map[string]any{"name": "Claimable Trial"}, admin)
	groupID := decode[struct {
		Group struct{ ID string } `json:"group"`
	}](t, groupRes).Group.ID

	created := in.do(http.MethodPost, "/api/admin/invites",
		map[string]any{
			"kind": "partner", "count": 1, "code": "CLAIMHTTP", "name": "Claim HTTP Partner",
			"group_id": groupID, "group_days": 9,
		}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create partner: %d %s", created.Code, created.Body.String())
	}

	badCode := in.do(http.MethodPost, "/api/profile/invites/claim", map[string]string{"code": "NOSUCHCODE"}, member)
	if badCode.Code != http.StatusBadRequest || errCode(t, badCode) != "invite_invalid" {
		t.Fatalf("claim a bad code: %d %s", badCode.Code, badCode.Body.String())
	}

	claim := in.do(http.MethodPost, "/api/profile/invites/claim", map[string]string{"code": "claim-http"}, member)
	if claim.Code != http.StatusOK {
		t.Fatalf("claim: %d %s", claim.Code, claim.Body.String())
	}
	result := decode[struct {
		GroupID   string `json:"group_id"`
		GroupName string `json:"group_name"`
		Days      int64  `json:"days"`
		ExpiresAt int64  `json:"expires_at"`
	}](t, claim)
	if result.GroupID != groupID || result.GroupName != "Claimable Trial" || result.Days != 9 || result.ExpiresAt == 0 {
		t.Fatalf("claim result = %+v", result)
	}

	account := decode[registerResponse](t, in.do(http.MethodGet, "/api/auth/me", nil, member)).User
	if account.GroupID != groupID || account.GroupExpiresAt != result.ExpiresAt {
		t.Fatalf("membership after claiming: %+v", account)
	}

	again := in.do(http.MethodPost, "/api/profile/invites/claim", map[string]string{"code": "CLAIM-HTTP"}, member)
	if again.Code != http.StatusConflict || errCode(t, again) != "invite_claimed" {
		t.Fatalf("claim again: %d %s", again.Code, again.Body.String())
	}

	uses := in.do(http.MethodGet, "/api/admin/invites/"+decode[struct {
		Codes []struct{ ID string } `json:"codes"`
	}](t, created).Codes[0].ID+"/uses", nil, admin)
	if uses.Code != http.StatusOK {
		t.Fatalf("uses: %d %s", uses.Code, uses.Body.String())
	}
	rows := decode[struct {
		Uses []struct {
			UserID string `json:"user_id"`
			Via    string `json:"via"`
		} `json:"uses"`
	}](t, uses).Uses
	if len(rows) != 1 || rows[0].UserID != member.userID || rows[0].Via != "claim" {
		t.Fatalf("uses rows = %+v, want one claim row for %s", rows, member.userID)
	}

	stats := in.do(http.MethodGet, "/api/admin/invites/stats", nil, admin)
	if stats.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", stats.Code, stats.Body.String())
	}
	partners := decode[struct {
		Partners []struct {
			Code          string `json:"code"`
			Claims        int    `json:"claims"`
			Registrations int    `json:"registrations"`
		} `json:"partners"`
	}](t, stats).Partners
	if len(partners) != 1 || partners[0].Claims != 1 || partners[0].Registrations != 0 {
		t.Fatalf("partner stats = %+v, want one partner with 1 claim and 0 registrations", partners)
	}
}
