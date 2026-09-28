package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The departure closure, exercised as HTTP: the bot's webhook from outside
// (no session, bearer token only), the backoffice's action from inside, and
// the account states each leaves behind.

// registerThroughInvite signs a new account up through an invite code and
// hands back its session, the way a real visitor arriving from an invite
// link would. The address is deliberately not the one in.do's default
// RemoteAddr produces: the inviter registered from that one, and an invitee
// arriving from the same address reads as same_ip to the reward check —
// which would skip the very reward these tests mean to claw back.
func (in *instance) registerThroughInvite(t *testing.T, code, username string) *session {
	t.Helper()
	response := in.doFrom("198.51.100.77", "test-agent", http.MethodPost, "/api/auth/register",
		map[string]string{"username": username, "password": "a-good-password", "invite_code": code}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("register %s through %s: %d %s", username, code, response.Code, response.Body.String())
	}
	var payload struct {
		User struct{ ID string } `json:"user"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "obsidian_session" && cookie.Value != "" {
			return &session{cookie: cookie, userID: payload.User.ID}
		}
	}
	t.Fatalf("register %s returned no session cookie", username)
	return nil
}

// doBot posts to the webhook the way the bot does: no session, a bearer
// header, a body.
func (in *instance) doBot(t *testing.T, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/bot/departure", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, request)
	return recorder
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

// departSetup turns an admin into an inviter whose invitee has been rewarded
// one card, and hands the invitee a QQ number — the full shape a departure
// then closes.
func (in *instance) departSetup(t *testing.T) (admin, invitee *session) {
	t.Helper()
	admin = in.register("founder", "a-good-password")
	in.setInviteSettings(t, map[string]string{
		settings.RegistrationEnabled: "true", settings.InvitesUserEnabled: "true",
		settings.InvitesRewardCards: "1", settings.InvitesUserLimit: "0",
	})
	profile := in.do(http.MethodGet, "/api/profile/invites", nil, admin)
	if profile.Code != http.StatusOK {
		t.Fatalf("read profile invites: %d %s", profile.Code, profile.Body.String())
	}
	code := decode[struct {
		Code string `json:"code"`
	}](t, profile).Code
	if code == "" {
		t.Fatal("admin has no personal code")
	}
	invitee = in.registerThroughInvite(t, code, "departing-invitee")
	if _, err := in.db.Exec(t.Context(),
		`UPDATE users SET qq = ? WHERE id = ?`, "12345678", invitee.userID); err != nil {
		t.Fatalf("stamp qq: %v", err)
	}
	if err := in.server.settings.SetMany(t.Context(), map[string]string{
		settings.BotWebhookToken: testBotToken,
	}); err != nil {
		t.Fatalf("set bot token: %v", err)
	}
	return admin, invitee
}

func TestBotDepartureWebhook(t *testing.T) {
	in := newInstance(t)
	admin, invitee := in.departSetup(t)

	// No token, wrong token: the same flat refusal either way, whether the
	// route is switched off or the token is merely bad.
	for _, token := range []string{"", "not-the-token"} {
		res := in.doBot(t, token, map[string]string{"qq": "12345678"})
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("webhook with token %q: %d %s", token, res.Code, res.Body.String())
		}
	}

	res := in.doBot(t, testBotToken, map[string]string{"qq": "87654321"})
	if res.Code != http.StatusOK {
		t.Fatalf("webhook for an unknown qq: %d %s", res.Code, res.Body.String())
	}
	if got := decode[botDepartureResponse](t, res).Status; got != "unknown" {
		t.Fatalf("unknown qq status = %q, want unknown", got)
	}

	// The real event, mode omitted: the instance default (disable) decides.
	res = in.doBot(t, testBotToken, map[string]string{"qq": "12345678"})
	if res.Code != http.StatusOK {
		t.Fatalf("webhook departure: %d %s", res.Code, res.Body.String())
	}
	outcome := decode[botDepartureResponse](t, res)
	if outcome.Status != "departed" {
		t.Fatalf("departure status = %q, want departed", outcome.Status)
	}
	if outcome.Result.Mode != settings.DepartModeDisable {
		t.Fatalf("default mode = %q, want disable", outcome.Result.Mode)
	}
	if outcome.Result.CardsDue != 1 || outcome.Result.CardsRevoked != 1 {
		t.Fatalf("due/revoked = %d/%d, want 1/1", outcome.Result.CardsDue, outcome.Result.CardsRevoked)
	}

	// The session the invitee was holding died with the account.
	if res := in.do(http.MethodGet, "/api/auth/me", nil, invitee); res.Code != http.StatusUnauthorized {
		t.Fatalf("invitee session after disable: %d", res.Code)
	}
	// The reward was clawed from the inviter.
	cards := in.do(http.MethodGet, "/api/usage/cards", nil, admin)
	held := decode[struct {
		Cards []any `json:"cards"`
	}](t, cards).Cards
	if len(held) != 0 {
		t.Fatalf("inviter cards after claw-back = %d, want 0", len(held))
	}
	// The inviter was told, after the commit.
	var notices int
	if err := in.db.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND kind = ?`,
		admin.userID, "invite_departed").Scan(&notices); err != nil {
		t.Fatalf("read notifications: %v", err)
	}
	if notices != 1 {
		t.Fatalf("departure notices = %d, want 1", notices)
	}

	// A redelivered event reads as already departed, not as a second
	// claw-back or a mystery.
	res = in.doBot(t, testBotToken, map[string]string{"qq": "12345678"})
	if got := decode[botDepartureResponse](t, res).Status; got != "already_departed" {
		t.Fatalf("redelivered status = %q, want already_departed", got)
	}
}

func TestBotDepartureWebhookRateLimit(t *testing.T) {
	in := newInstance(t)
	in.departSetup(t)

	// Burst 10: twelve rapid reports from one address and some of them are
	// told to slow down. The unknown-QQ lookups keep every call cheap while
	// still crossing the limit.
	var limited int
	for range 12 {
		res := in.doBot(t, testBotToken, map[string]string{"qq": "87654321"})
		if res.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("twelve rapid webhook calls never hit the rate limit")
	}
}

func TestBotDepartureDeleteModeByEvent(t *testing.T) {
	in := newInstance(t)
	in.departSetup(t)

	res := in.doBot(t, testBotToken, map[string]string{"qq": "12345678", "mode": "delete"})
	if res.Code != http.StatusOK {
		t.Fatalf("webhook delete departure: %d %s", res.Code, res.Body.String())
	}
	outcome := decode[botDepartureResponse](t, res)
	if outcome.Status != "departed" || outcome.Result.Mode != "delete" {
		t.Fatalf("status/mode = %s/%s, want departed/delete", outcome.Status, outcome.Result.Mode)
	}
	// The cascade took the account, and with it the QQ the invitee held:
	// a fresh registration with the same number is possible again, which is
	// the accepted price of the delete mode.
	var gone int
	if err := in.db.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM users WHERE qq = ?`, "12345678").Scan(&gone); err != nil {
		t.Fatalf("count users by qq: %v", err)
	}
	if gone != 0 {
		t.Fatal("the deleted account still holds its QQ number")
	}
}

func TestAdminDepartureEndpoint(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	in.setInviteSettings(t, map[string]string{settings.RegistrationEnabled: "true"})

	first := in.register("leaving-one", "a-good-password")
	second := in.register("leaving-two", "a-good-password")

	// Disable keeps the account listed, but ended: its session is dead and
	// a second attempt refuses rather than repeating itself.
	res := in.do(http.MethodPost, "/api/admin/users/"+first.userID+"/departure",
		map[string]string{"mode": "disable", "note": "left the group"}, admin)
	if res.Code != http.StatusOK {
		t.Fatalf("departure disable: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodGet, "/api/auth/me", nil, first); res.Code != http.StatusUnauthorized {
		t.Fatalf("session after disable: %d", res.Code)
	}
	if res := in.do(http.MethodPost, "/api/admin/users/"+first.userID+"/departure",
		map[string]string{"mode": "disable"}, admin); errCode(t, res) != "already_departed" {
		t.Fatalf("second departure: %d %s", res.Code, res.Body.String())
	}

	// Delete removes the account outright; the tombstone still answers the
	// retry with "already departed".
	res = in.do(http.MethodPost, "/api/admin/users/"+second.userID+"/departure",
		map[string]string{"mode": "delete"}, admin)
	if res.Code != http.StatusOK {
		t.Fatalf("departure delete: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodGet, "/api/auth/me", nil, second); res.Code != http.StatusUnauthorized {
		t.Fatalf("session after delete: %d", res.Code)
	}
	if errCode(t, in.do(http.MethodPost, "/api/admin/users/"+second.userID+"/departure",
		map[string]string{"mode": "delete"}, admin)) != "already_departed" {
		t.Fatal("re-departing a deleted account did not read the tombstone")
	}

	// The audit list shows both, newest first, and an operator cannot point
	// the action at themselves.
	list := in.do(http.MethodGet, "/api/admin/departures", nil, admin)
	departures := decode[struct {
		Departures []struct {
			Username string `json:"username"`
			Mode     string `json:"mode"`
		} `json:"departures"`
		Total int `json:"total"`
	}](t, list)
	if departures.Total != 2 || len(departures.Departures) != 2 {
		t.Fatalf("departures = %d/%d, want 2/2", len(departures.Departures), departures.Total)
	}
	self := in.do(http.MethodPost, "/api/admin/users/"+admin.userID+"/departure",
		map[string]string{"mode": "disable"}, admin)
	if self.Code != http.StatusBadRequest {
		t.Fatalf("self departure: %d, want 400", self.Code)
	}
}
