package server

import (
	"net/http"
	"strings"
	"testing"
)

// A ban is a status and a reason together, and the reason only ever stands on
// a banned account. These hold the reason to the lines the status keeps: it
// is trimmed and capped, it is what the account is shown when it tries to sign
// in, and it is gone the moment the account is not banned — whatever the
// request carried, and whichever road it came by.

type banView struct {
	User struct {
		Status    string `json:"status"`
		BanReason string `json:"ban_reason"`
	} `json:"user"`
}

func TestBanningWithAReasonShowsItAtSignIn(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	victim := in.register("visitor", "another-password")

	ban := in.do(http.MethodPatch, "/api/admin/users/"+victim.userID,
		map[string]any{"status": "disabled", "ban_reason": "  Abuse of resources  "}, founder)
	if ban.Code != http.StatusOK {
		t.Fatalf("ban: %d %s", ban.Code, ban.Body.String())
	}
	if got := decode[banView](t, ban); got.User.Status != "disabled" || got.User.BanReason != "Abuse of resources" {
		t.Fatalf("after the ban: status %q, reason %q; want disabled and the trimmed reason", got.User.Status, got.User.BanReason)
	}

	detail := in.do(http.MethodGet, "/api/admin/users/"+victim.userID, nil, founder)
	if got := decode[banView](t, detail); got.User.BanReason != "Abuse of resources" {
		t.Fatalf("the account detail reads reason %q, want the one the ban stored", got.User.BanReason)
	}

	refused := in.do(http.MethodPost, "/api/auth/login",
		map[string]string{"identifier": "visitor", "password": "another-password"}, nil)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("sign-in while banned: %d %s", refused.Code, refused.Body.String())
	}
	body := decode[struct {
		Error struct {
			Code      string `json:"code"`
			BanReason string `json:"ban_reason"`
		} `json:"error"`
	}](t, refused)
	if body.Error.Code != "account_banned" || body.Error.BanReason != "Abuse of resources" {
		t.Fatalf("sign-in refusal = %+v, want account_banned carrying the reason", body.Error)
	}
}

func TestUnbanningClearsTheReasonAndSignInWorksAgain(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	victim := in.register("visitor", "another-password")
	path := "/api/admin/users/" + victim.userID

	if response := in.do(http.MethodPatch, path,
		map[string]any{"status": "disabled", "ban_reason": "Abuse"}, founder); response.Code != http.StatusOK {
		t.Fatalf("ban: %d %s", response.Code, response.Body.String())
	}

	unban := in.do(http.MethodPatch, path, map[string]any{"status": "active"}, founder)
	if unban.Code != http.StatusOK {
		t.Fatalf("unban: %d %s", unban.Code, unban.Body.String())
	}
	if got := decode[banView](t, unban); got.User.Status != "active" || got.User.BanReason != "" {
		t.Fatalf("after the unban: status %q, reason %q; want active and no reason", got.User.Status, got.User.BanReason)
	}

	if response := in.do(http.MethodPost, "/api/auth/login",
		map[string]string{"identifier": "visitor", "password": "another-password"}, nil); response.Code != http.StatusOK {
		t.Fatalf("sign-in after the unban: %d %s", response.Code, response.Body.String())
	}
}

// Whatever the request says about the status, an account that is not banned
// after it has no reason, and a later ban without one does not inherit an old
// reason.
func TestAReasonOnlyStandsOnABannedAccount(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	member := in.register("member", "another-password")
	path := "/api/admin/users/" + member.userID

	reasonOf := func(body map[string]any) string {
		t.Helper()
		response := in.do(http.MethodPatch, path, body, founder)
		if response.Code != http.StatusOK {
			t.Fatalf("PATCH %v: %d %s", body, response.Code, response.Body.String())
		}
		return decode[banView](t, response).User.BanReason
	}

	if got := reasonOf(map[string]any{"ban_reason": "sneaky"}); got != "" {
		t.Errorf("a reason sent to an active account is kept: %q", got)
	}
	if got := reasonOf(map[string]any{"status": "active", "ban_reason": "sneaky"}); got != "" {
		t.Errorf("a reason sent with status active is kept: %q", got)
	}
	if got := reasonOf(map[string]any{"status": "disabled"}); got != "" {
		t.Errorf("a fresh ban without a reason carries one: %q", got)
	}
	if got := reasonOf(map[string]any{"status": "disabled", "ban_reason": "first"}); got != "first" {
		t.Errorf("a ban with a reason stored %q", got)
	}
	// A ban that does not restate its reason leaves the one already there.
	if got := reasonOf(map[string]any{"status": "disabled"}); got != "first" {
		t.Errorf("re-stating the ban without a reason dropped the reason: %q", got)
	}
	if got := reasonOf(map[string]any{"ban_reason": "second"}); got != "second" {
		t.Errorf("a reason sent alone to a banned account was dropped: %q", got)
	}
	if got := reasonOf(map[string]any{"status": "active"}); got != "" {
		t.Errorf("unbanning kept the reason: %q", got)
	}
	// A reason alone is kept only while the account is banned, which it is not now.
	if got := reasonOf(map[string]any{"ban_reason": "late"}); got != "" {
		t.Errorf("a reason sent alone to an unbanned account is kept: %q", got)
	}
}

func TestBanReasonIsCappedAfterTrimming(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	victim := in.register("visitor", "another-password")
	path := "/api/admin/users/" + victim.userID

	// The cap counts characters, not bytes: each of these is three bytes.
	tooLong := strings.Repeat("理", 501)
	if response := in.do(http.MethodPatch, path,
		map[string]any{"status": "disabled", "ban_reason": tooLong}, founder); response.Code != http.StatusBadRequest {
		t.Fatalf("a 501-character reason: %d %s, want 400", response.Code, response.Body.String())
	}

	// Surrounding spaces are not part of the reason, so they do not count
	// against the cap and are not kept.
	atCap := strings.Repeat("理", 500)
	response := in.do(http.MethodPatch, path,
		map[string]any{"status": "disabled", "ban_reason": "  " + atCap + "  "}, founder)
	if response.Code != http.StatusOK {
		t.Fatalf("a 500-character reason with spaces around it: %d %s", response.Code, response.Body.String())
	}
	if got := decode[banView](t, response).User.BanReason; got != atCap {
		t.Fatalf("stored %d characters, want the 500 kept without the spaces", len([]rune(got)))
	}
}

func TestAnAdministratorCannotBanTheirOwnAccount(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	second := in.register("second-admin", "another-password")
	if response := in.do(http.MethodPatch, "/api/admin/users/"+second.userID,
		map[string]any{"role": "super_admin"}, founder); response.Code != http.StatusOK {
		t.Fatalf("promote second administrator: %d %s", response.Code, response.Body.String())
	}

	// With a second super administrator the last-administrator rule does not
	// apply, so this is the self-ban refusal on its own.
	ban := in.do(http.MethodPatch, "/api/admin/users/"+founder.userID,
		map[string]any{"status": "disabled", "ban_reason": "myself"}, founder)
	if ban.Code != http.StatusBadRequest || !strings.Contains(ban.Body.String(), "own account") {
		t.Fatalf("banning oneself: %d %s, want 400 refusing one's own account", ban.Code, ban.Body.String())
	}
	if code := in.do(http.MethodGet, "/api/auth/me", nil, founder).Code; code != http.StatusOK {
		t.Errorf("the refused self-ban still signed the administrator out: %d", code)
	}
}

// A users grant bans ordinary accounts with a reason and nothing above them:
// the rules that held for the status alone hold for the reason too.
func TestADelegatedUsersGrantBansOrdinaryAccountsWithAReason(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	member := in.register("member", "a-good-password")
	colleague := in.register("colleague", "a-good-password")
	if response := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"users"}}, founder); response.Code != http.StatusOK {
		t.Fatalf("grant users: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodPatch, "/api/admin/users/"+colleague.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"settings"}}, founder); response.Code != http.StatusOK {
		t.Fatalf("make a colleague an administrator: %d %s", response.Code, response.Body.String())
	}

	ban := in.do(http.MethodPatch, "/api/admin/users/"+member.userID,
		map[string]any{"status": "disabled", "ban_reason": "spam"}, operator)
	if ban.Code != http.StatusOK {
		t.Fatalf("a users grant banning an ordinary account: %d %s", ban.Code, ban.Body.String())
	}
	if got := decode[banView](t, ban); got.User.BanReason != "spam" {
		t.Errorf("reason stored as %q, want spam", got.User.BanReason)
	}

	for _, target := range []*session{colleague, founder} {
		response := in.do(http.MethodPatch, "/api/admin/users/"+target.userID,
			map[string]any{"status": "disabled", "ban_reason": "not mine"}, operator)
		if response.Code != http.StatusForbidden {
			t.Errorf("a users grant banning an administrator: %d %s, want 403", response.Code, response.Body.String())
		}
	}
}

func TestCreatingAnAccountKeepsAReasonOnlyWhenItIsBanned(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	banned := in.do(http.MethodPost, "/api/admin/users",
		map[string]any{"username": "troll", "status": "disabled", "ban_reason": "  spam  "}, founder)
	if banned.Code != http.StatusCreated {
		t.Fatalf("create banned: %d %s", banned.Code, banned.Body.String())
	}
	if got := decode[banView](t, banned); got.User.Status != "disabled" || got.User.BanReason != "spam" {
		t.Errorf("created banned: status %q, reason %q; want disabled and the trimmed reason", got.User.Status, got.User.BanReason)
	}

	active := in.do(http.MethodPost, "/api/admin/users",
		map[string]any{"username": "member", "status": "active", "ban_reason": "sneaky"}, founder)
	if active.Code != http.StatusCreated {
		t.Fatalf("create active: %d %s", active.Code, active.Body.String())
	}
	if got := decode[banView](t, active); got.User.BanReason != "" {
		t.Errorf("created active keeps reason %q", got.User.BanReason)
	}

	tooLong := in.do(http.MethodPost, "/api/admin/users",
		map[string]any{"username": "tooLong", "status": "disabled", "ban_reason": strings.Repeat("x", 501)}, founder)
	if tooLong.Code != http.StatusBadRequest {
		t.Errorf("create with a 501-character reason: %d %s, want 400", tooLong.Code, tooLong.Body.String())
	}
}

// The console is a second door onto the same endpoint, so the flag has to be
// as strict there as the form is: a reason only goes with a ban in the same
// command, and a banned account made from the console carries its reason.
func TestTheConsoleBansWithAReasonAndRefusesAStrayOne(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	victim := in.register("visitor", "another-password")

	out, verdict := consoleRun(t, in, founder, "user edit visitor --reason 'spam'")
	if verdict.OK {
		t.Fatalf("a reason without --status disabled was accepted: %q", out)
	}
	if got := decode[banView](t, in.do(http.MethodGet, "/api/admin/users/"+victim.userID, nil, founder)); got.User.Status != "active" || got.User.BanReason != "" {
		t.Fatalf("the refused command still changed the account: %q, %q", got.User.Status, got.User.BanReason)
	}

	if out, verdict = consoleRun(t, in, founder, "user edit visitor --status disabled --reason 'spamming the group'"); !verdict.OK {
		t.Fatalf("ban from the console: %q", out)
	}
	if got := decode[banView](t, in.do(http.MethodGet, "/api/admin/users/"+victim.userID, nil, founder)); got.User.BanReason != "spamming the group" {
		t.Errorf("console ban stored %q, want the reason typed", got.User.BanReason)
	}

	if out, verdict = consoleRun(t, in, founder, "user create troll --status disabled --reason 'bot'"); !verdict.OK {
		t.Fatalf("create banned from the console: %q", out)
	}
	page := decode[struct {
		Users []struct {
			Username  string `json:"username"`
			BanReason string `json:"ban_reason"`
		} `json:"users"`
	}](t, in.do(http.MethodGet, "/api/admin/users?q=troll", nil, founder))
	if len(page.Users) != 1 || page.Users[0].BanReason != "bot" {
		t.Errorf("console-created banned account: %+v, want one account with reason bot", page.Users)
	}
}

// A key minted before the ban still works as a key, so the API says what
// sign-in says: banned, and why — not that the key is wrong.
func TestABannedAccountsKeyIsToldTheReason(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	victim := in.register("visitor", "another-password")
	in.enableAPI(founder)
	token := in.mintKey(victim)

	if ban := in.do(http.MethodPatch, "/api/admin/users/"+victim.userID,
		map[string]any{"status": "disabled", "ban_reason": "滥用资源"}, founder); ban.Code != http.StatusOK {
		t.Fatalf("ban: %d %s", ban.Code, ban.Body.String())
	}

	refused := in.doWithKey(http.MethodGet, "/v1/models", token)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("a banned account's key: %d %s", refused.Code, refused.Body.String())
	}
	body := decode[struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}](t, refused)
	if body.Error.Code != "account_banned" || !strings.Contains(body.Error.Message, "滥用资源") {
		t.Fatalf("API refusal = %+v, want account_banned carrying the reason", body.Error)
	}

	// A key nobody issued still answers as a bad key, whatever is banned.
	if code := in.doWithKey(http.MethodGet, "/v1/models", "sk-not-a-real-key").Code; code != http.StatusUnauthorized {
		t.Fatalf("an unknown key: %d, want 401", code)
	}
}
