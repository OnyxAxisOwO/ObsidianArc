package qqgroup

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/server/servertest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The departure closure, exercised as HTTP against a server with this plugin
// compiled in: the bot's webhook from outside (no session, bearer token
// only), the backoffice's action from inside, and the account states each
// leaves behind.

// registerThroughInvite signs a new account up through an invite code. The
// address is deliberately not the default one: the inviter registered from
// that, and an invitee arriving from the same address reads as same_ip to
// the reward check — which would skip the very reward these tests mean to
// claw back.
func registerThroughInvite(in *servertest.Instance, code, username string, fields map[string]string) *servertest.Session {
	in.T.Helper()
	body := map[string]any{"username": username, "password": "a-good-password", "invite_code": code}
	if fields != nil {
		body["fields"] = fields
	}
	response := in.DoFrom("198.51.100.77", http.MethodPost, "/api/auth/register", body, nil)
	if response.Code != http.StatusCreated {
		in.T.Fatalf("register %s through %s: %d %s", username, code, response.Code, response.Body.String())
	}
	var session *servertest.Session
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "obsidian_session" && cookie.Value != "" {
			session = &servertest.Session{Cookie: cookie}
		}
	}
	session.UserID = servertest.Decode[struct {
		User struct{ ID string } `json:"user"`
	}](in.T, response).User.ID
	return session
}

func doBot(in *servertest.Instance, token string, body any) *httptest.ResponseRecorder {
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	return in.DoWith(http.MethodPost, "/api/bot/departure", body, nil, header)
}

type botDepartureResponse struct {
	Status string `json:"status"`
	Result struct {
		Mode         string `json:"mode"`
		CardsDue     int    `json:"cards_due"`
		CardsRevoked int    `json:"cards_revoked"`
		InviterID    string `json:"inviter_id"`
	} `json:"result"`
}

const testBotToken = "a-webhook-token-a-bot-keeps"

// httpDepartSetup turns an admin into an inviter whose invitee — carrying a QQ
// number — has been rewarded one card: the full shape a departure closes.
func httpDepartSetup(t *testing.T, in *servertest.Instance) (admin, invitee *servertest.Session) {
	t.Helper()
	admin = in.Register("founder", "a-good-password")
	in.SetSettings(admin, map[string]string{
		settings.RegistrationEnabled: "true", settings.InvitesUserEnabled: "true",
		settings.InvitesRewardCards: "1", settings.InvitesUserLimit: "0",
		BotWebhookToken: testBotToken,
	})
	profile := in.Do(http.MethodGet, "/api/profile/invites", nil, admin)
	code := servertest.Decode[struct {
		Code string `json:"code"`
	}](t, profile).Code
	if code == "" {
		t.Fatalf("admin has no personal code: %s", profile.Body.String())
	}
	invitee = registerThroughInvite(in, code, "departing-invitee", map[string]string{Field: "12345678"})
	return admin, invitee
}

func TestBotDepartureWebhook(t *testing.T) {
	in := servertest.New(t)
	admin, invitee := httpDepartSetup(t, in)

	// No token, wrong token: the same flat refusal either way, whether the
	// route is switched off or the token is merely bad.
	for _, token := range []string{"", "not-the-token"} {
		res := doBot(in, token, map[string]string{"qq": "12345678"})
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("webhook with token %q: %d %s", token, res.Code, res.Body.String())
		}
	}

	res := doBot(in, testBotToken, map[string]string{"qq": "87654321"})
	if got := servertest.Decode[botDepartureResponse](t, res).Status; res.Code != http.StatusOK || got != "unknown" {
		t.Fatalf("unknown qq: %d %s", res.Code, res.Body.String())
	}

	// The real event, mode omitted: the instance default (disable) decides.
	res = doBot(in, testBotToken, map[string]string{"qq": "12345678"})
	outcome := servertest.Decode[botDepartureResponse](t, res)
	if res.Code != http.StatusOK || outcome.Status != "departed" || outcome.Result.Mode != ModeDisable {
		t.Fatalf("departure: %d %s", res.Code, res.Body.String())
	}
	if outcome.Result.CardsDue != 1 || outcome.Result.CardsRevoked != 1 {
		t.Fatalf("due/revoked = %d/%d, want 1/1", outcome.Result.CardsDue, outcome.Result.CardsRevoked)
	}

	// The session the invitee was holding died with the account.
	if res := in.Do(http.MethodGet, "/api/auth/me", nil, invitee); res.Code != http.StatusUnauthorized {
		t.Fatalf("invitee session after disable: %d", res.Code)
	}
	// The reward was clawed from the inviter.
	held := servertest.Decode[struct {
		Cards []any `json:"cards"`
	}](t, in.Do(http.MethodGet, "/api/usage/cards", nil, admin)).Cards
	if len(held) != 0 {
		t.Fatalf("inviter cards after claw-back = %d, want 0", len(held))
	}
	// The inviter was told, after the commit.
	var notices int
	if err := in.DB.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND kind = ?`,
		admin.UserID, "invite_departed").Scan(&notices); err != nil {
		t.Fatalf("read notifications: %v", err)
	}
	if notices != 1 {
		t.Fatalf("departure notices = %d, want 1", notices)
	}
	// And the inviter's own list badges the invitee who left.
	list := in.Do(http.MethodGet, "/api/profile/invites", nil, admin)
	if !strings.Contains(list.Body.String(), `"departed":true`) ||
		!strings.Contains(list.Body.String(), `"departure_mode":"disable"`) {
		t.Fatalf("the invitee list does not badge the departure: %s", list.Body.String())
	}

	// A redelivered event reads as already departed, not as a second
	// claw-back or a mystery.
	res = doBot(in, testBotToken, map[string]string{"qq": "12345678"})
	if got := servertest.Decode[botDepartureResponse](t, res).Status; got != "already_departed" {
		t.Fatalf("redelivered status = %q, want already_departed", got)
	}
}

func TestBotDepartureWebhookRateLimit(t *testing.T) {
	in := servertest.New(t)
	httpDepartSetup(t, in)

	// Burst 10: twelve rapid reports from one address and some of them are
	// told to slow down. The unknown-QQ lookups keep every call cheap while
	// still crossing the limit.
	limited := 0
	for range 12 {
		if doBot(in, testBotToken, map[string]string{"qq": "87654321"}).Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("twelve rapid webhook calls never hit the rate limit")
	}
}

func TestBotDepartureDeleteModeByEvent(t *testing.T) {
	in := servertest.New(t)
	admin, _ := httpDepartSetup(t, in)

	res := doBot(in, testBotToken, map[string]string{"qq": "12345678", "mode": "delete"})
	outcome := servertest.Decode[botDepartureResponse](t, res)
	if res.Code != http.StatusOK || outcome.Status != "departed" || outcome.Result.Mode != ModeDelete {
		t.Fatalf("delete departure: %d %s", res.Code, res.Body.String())
	}
	// The cascade took the account, and with it the QQ the invitee held:
	// a fresh registration with the same number is possible again, which is
	// the accepted price of the delete mode.
	var holders int
	if err := in.DB.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM users WHERE qq = ?`, "12345678").Scan(&holders); err != nil {
		t.Fatalf("count users by qq: %v", err)
	}
	if holders != 0 {
		t.Fatal("the deleted account still holds its QQ number")
	}
	// The deleted invitee is still in the inviter's list, from the tombstone.
	list := in.Do(http.MethodGet, "/api/profile/invites", nil, admin)
	if !strings.Contains(list.Body.String(), `"username":"departing-invitee"`) ||
		!strings.Contains(list.Body.String(), `"departure_mode":"delete"`) {
		t.Fatalf("the deleted invitee vanished from the list: %s", list.Body.String())
	}
}

func TestAdminDepartureEndpoint(t *testing.T) {
	in := servertest.New(t)
	admin := in.Register("founder", "a-good-password")
	first := in.Register("leaving-one", "a-good-password")
	second := in.Register("leaving-two", "a-good-password")

	// Neither route answers an account without the grant.
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/admin/users/" + second.UserID + "/departure"},
		{http.MethodGet, "/api/admin/departures"},
	} {
		if res := in.Do(route.method, route.path, map[string]string{"mode": "disable"}, first); res.Code != http.StatusForbidden {
			t.Fatalf("%s %s as a regular account: %d %s", route.method, route.path, res.Code, res.Body.String())
		}
		if res := in.Do(route.method, route.path, map[string]string{"mode": "disable"}, nil); res.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s signed out: %d", route.method, route.path, res.Code)
		}
	}

	// Disable keeps the account listed, but ended: its session is dead and
	// a second attempt refuses rather than repeating itself.
	res := in.Do(http.MethodPost, "/api/admin/users/"+first.UserID+"/departure",
		map[string]string{"mode": "disable", "note": "left the group"}, admin)
	if res.Code != http.StatusOK {
		t.Fatalf("departure disable: %d %s", res.Code, res.Body.String())
	}
	if res := in.Do(http.MethodGet, "/api/auth/me", nil, first); res.Code != http.StatusUnauthorized {
		t.Fatalf("session after disable: %d", res.Code)
	}
	if res := in.Do(http.MethodPost, "/api/admin/users/"+first.UserID+"/departure",
		map[string]string{"mode": "disable"}, admin); servertest.ErrorCode(t, res) != "already_departed" {
		t.Fatalf("second departure: %d %s", res.Code, res.Body.String())
	}

	// Delete removes the account outright; the tombstone still answers the
	// retry with "already departed".
	res = in.Do(http.MethodPost, "/api/admin/users/"+second.UserID+"/departure",
		map[string]string{"mode": "delete"}, admin)
	if res.Code != http.StatusOK {
		t.Fatalf("departure delete: %d %s", res.Code, res.Body.String())
	}
	if servertest.ErrorCode(t, in.Do(http.MethodPost, "/api/admin/users/"+second.UserID+"/departure",
		map[string]string{"mode": "delete"}, admin)) != "already_departed" {
		t.Fatal("re-departing a deleted account did not read the tombstone")
	}

	// The audit list shows both, and an operator cannot point the action at
	// themselves.
	departures := servertest.Decode[struct {
		Departures []struct {
			Username string `json:"username"`
		} `json:"departures"`
		Total int `json:"total"`
	}](t, in.Do(http.MethodGet, "/api/admin/departures", nil, admin))
	if departures.Total != 2 || len(departures.Departures) != 2 {
		t.Fatalf("departures = %d/%d, want 2/2", len(departures.Departures), departures.Total)
	}
	if res := in.Do(http.MethodPost, "/api/admin/users/"+admin.UserID+"/departure",
		map[string]string{"mode": "disable"}, admin); res.Code != http.StatusBadRequest {
		t.Fatalf("self departure: %d, want 400", res.Code)
	}
	// An unknown mode is refused rather than defaulted.
	if res := in.Do(http.MethodPost, "/api/admin/users/"+admin.UserID+"/departure",
		map[string]string{"mode": "vanish"}, admin); res.Code != http.StatusBadRequest {
		t.Fatalf("unknown mode: %d, want 400", res.Code)
	}
}

// The QQ number as an account field: the sign-up form's rule follows the
// setting, the value is checked and unique, the owner can change it, and the
// backoffice finds an account by it.
func TestQQNumberIsAnAccountField(t *testing.T) {
	in := servertest.New(t)
	admin := in.Register("founder", "a-good-password")

	type site struct {
		Fields map[string]string `json:"fields"`
	}
	if got := servertest.Decode[site](t, in.Do(http.MethodGet, "/api/site", nil, nil)).Fields[Field]; got != "off" {
		t.Fatalf("default rule = %q, want off", got)
	}
	in.SetSettings(admin, map[string]string{Requirement: "required"})
	if got := servertest.Decode[site](t, in.Do(http.MethodGet, "/api/site", nil, nil)).Fields[Field]; got != "required" {
		t.Fatalf("rule after saving = %q, want required", got)
	}
	if res := in.Do(http.MethodPut, "/api/admin/settings", map[string]string{Requirement: "sometimes"}, admin); res.Code != http.StatusBadRequest {
		t.Fatalf("an unknown rule was accepted: %d", res.Code)
	}

	for _, attempt := range []struct {
		fields map[string]string
		code   string
	}{
		{nil, "qq_required"},
		{map[string]string{Field: "0123"}, "invalid_qq"},
	} {
		body := map[string]any{"username": "someone", "password": "a-good-password"}
		if attempt.fields != nil {
			body["fields"] = attempt.fields
		}
		if got := servertest.ErrorCode(t, in.Do(http.MethodPost, "/api/auth/register", body, nil)); got != attempt.code {
			t.Fatalf("register with %v: code %q, want %q", attempt.fields, got, attempt.code)
		}
	}
	member := in.RegisterWith(map[string]any{
		"username": "member", "password": "a-good-password", "fields": map[string]string{Field: "10001"},
	})
	if got := servertest.ErrorCode(t, in.Do(http.MethodPost, "/api/auth/register", map[string]any{
		"username": "copycat", "password": "a-good-password", "fields": map[string]string{Field: "10001"},
	}, nil)); got != "qq_taken" {
		t.Fatalf("a held number: code %q, want qq_taken", got)
	}

	me := servertest.Decode[struct {
		User struct {
			Fields map[string]string `json:"fields"`
		} `json:"user"`
	}](t, in.Do(http.MethodGet, "/api/auth/me", nil, member))
	if me.User.Fields[Field] != "10001" {
		t.Fatalf("the account does not carry its number: %+v", me.User)
	}
	// Required means it cannot be cleared, but it can be changed.
	if got := servertest.ErrorCode(t, in.Do(http.MethodPatch, "/api/profile",
		map[string]any{"fields": map[string]string{Field: ""}}, member)); got != "qq_required" {
		t.Fatalf("clearing a required number: code %q", got)
	}
	if res := in.Do(http.MethodPatch, "/api/profile",
		map[string]any{"fields": map[string]string{Field: "10002"}}, member); res.Code != http.StatusOK {
		t.Fatalf("changing the number: %d %s", res.Code, res.Body.String())
	}

	found := in.Do(http.MethodGet, "/api/admin/users?q=10002", nil, admin)
	if !strings.Contains(found.Body.String(), `"username":"member"`) {
		t.Fatalf("the backoffice search does not match the number: %s", found.Body.String())
	}
}
