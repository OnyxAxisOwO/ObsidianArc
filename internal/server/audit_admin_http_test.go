package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
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

// A users grant may write a limit on an ordinary account. It may not write one
// on a super administrator or on a peer it cannot manage, and the same goes for
// clearing one or resetting the account's quota. canPolicy alone decided this
// before, and it only looks at the grant.
func TestUserScopedLimitsFollowTheAdministratorRule(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	peer := in.register("peer", "a-good-password")
	member := in.register("member", "a-good-password")
	grantAdmin(t, in, founder, operator, "users")
	grantAdmin(t, in, founder, peer, "usage")

	limit := func(scopeID string) map[string]any {
		return map[string]any{
			"scope": "user", "scope_id": scopeID,
			"windows": map[string]any{"5h": map[string]any{"enabled": true, "tokens": 1000}},
		}
	}
	if res := in.do(http.MethodPut, "/api/admin/quota/policies", limit(founder.userID), operator); res.Code != http.StatusForbidden {
		t.Fatalf("users-only delegate limited a super administrator: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPut, "/api/admin/quota/policies", limit(peer.userID), operator); res.Code != http.StatusForbidden {
		t.Fatalf("users-only delegate limited a peer administrator: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPut, "/api/admin/quota/policies", limit(member.userID), operator); res.Code != http.StatusOK {
		t.Fatalf("ordinary account limit: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodDelete, "/api/admin/quota/policies/user?scope_id="+founder.userID, nil, operator); res.Code != http.StatusForbidden {
		t.Fatalf("users-only delegate cleared a super administrator's limit: %d %s", res.Code, res.Body.String())
	}
	reset := map[string]any{"scope": "user", "id": founder.userID}
	if res := in.do(http.MethodPost, "/api/admin/usage/reset", reset, peer); res.Code != http.StatusForbidden {
		t.Fatalf("usage delegate reset a super administrator's quota: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPost, "/api/admin/usage/reset", map[string]any{"scope": "user", "id": member.userID}, peer); res.Code != http.StatusOK {
		t.Fatalf("usage delegate reset an ordinary account: %d %s", res.Code, res.Body.String())
	}
	// A super administrator writes any of them.
	if res := in.do(http.MethodPut, "/api/admin/quota/policies", limit(peer.userID), founder); res.Code != http.StatusOK {
		t.Fatalf("super administrator limit: %d %s", res.Code, res.Body.String())
	}
}

// A bonus grant is money for the account it was made for. Revoking it takes
// that money back, so a usage delegate may not do it to an administrator it
// cannot manage, the way it may not write a limit on one.
func TestUsageDelegateCannotRevokeAnAdministratorsBonus(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	member := in.register("member", "a-good-password")
	grantAdmin(t, in, founder, operator, "usage")

	created := in.do(http.MethodPost, "/api/admin/bonus/bars", map[string]any{
		"name": "礼包", "toggle_mode": "user", "show_total": true,
	}, founder)
	if created.Code != http.StatusCreated {
		t.Fatalf("bar: %d %s", created.Code, created.Body.String())
	}
	bar := decode[struct {
		Bar struct{ ID string } `json:"bar"`
	}](t, created).Bar
	grant := func(who *session) string {
		t.Helper()
		res := in.do(http.MethodPost, "/api/admin/bonus/bars/"+bar.ID+"/grants",
			map[string]any{"user_ids": []string{who.userID}, "amount": 100, "days": 30}, founder)
		if res.Code != http.StatusCreated {
			t.Fatalf("grant: %d %s", res.Code, res.Body.String())
		}
		listed := decode[struct {
			Grants []struct {
				ID     string `json:"id"`
				UserID string `json:"user_id"`
			} `json:"grants"`
		}](t, in.do(http.MethodGet, "/api/admin/bonus/bars/"+bar.ID+"/grants", nil, founder))
		for _, row := range listed.Grants {
			if row.UserID == who.userID {
				return row.ID
			}
		}
		t.Fatalf("no grant for %s", who.userID)
		return ""
	}

	founderGrant := grant(founder)
	if res := in.do(http.MethodDelete, "/api/admin/bonus/grants/"+founderGrant, nil, operator); res.Code != http.StatusForbidden {
		t.Fatalf("usage delegate revoked a super administrator's bonus: %d %s", res.Code, res.Body.String())
	}
	memberGrant := grant(member)
	if res := in.do(http.MethodDelete, "/api/admin/bonus/grants/"+memberGrant, nil, operator); res.Code != http.StatusOK {
		t.Fatalf("usage delegate revoked an ordinary account's bonus: %d %s", res.Code, res.Body.String())
	}
}

// The groups page lists every group's limits in one response. A groups-only
// administrator gets the group-scoped ones and nothing scoped to an account,
// the same rows /api/admin/quota/policies would show that grant.
func TestGroupsGrantSeesNoAccountScopedLimits(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	member := in.register("member", "a-good-password")
	grantAdmin(t, in, founder, operator, "groups")

	groups := decode[struct {
		Groups []struct {
			ID string `json:"id"`
		} `json:"groups"`
	}](t, in.do(http.MethodGet, "/api/admin/groups", nil, founder))
	if len(groups.Groups) == 0 {
		t.Fatal("no group to scope a limit to")
	}
	limit := func(scope, scopeID string) {
		t.Helper()
		body := map[string]any{
			"scope": scope, "scope_id": scopeID,
			"windows": map[string]any{"5h": map[string]any{"enabled": true, "tokens": 1000}},
		}
		if res := in.do(http.MethodPut, "/api/admin/quota/policies", body, founder); res.Code != http.StatusOK {
			t.Fatalf("%s limit: %d %s", scope, res.Code, res.Body.String())
		}
	}
	limit("user", member.userID)
	limit("group", groups.Groups[0].ID)

	res := in.do(http.MethodGet, "/api/admin/groups", nil, operator)
	if res.Code != http.StatusOK {
		t.Fatalf("groups: %d %s", res.Code, res.Body.String())
	}
	payload := decode[struct {
		Policies []struct {
			Scope   string `json:"scope"`
			ScopeID string `json:"scope_id"`
		} `json:"policies"`
	}](t, res)
	if len(payload.Policies) != 1 || payload.Policies[0].Scope != "group" {
		t.Fatalf("groups-only administrator saw %+v", payload.Policies)
	}
	if strings.Contains(res.Body.String(), member.userID) {
		t.Fatalf("an account id leaked through the groups response: %s", res.Body.String())
	}
}

// A dashboard grant sees the instance's totals and its newest accounts. The
// per-account rankings and the newest requests name who spent what, which is
// the usage grant's data; a dashboard-only administrator gets empty lists in
// those two places.
func TestDashboardGrantSeesNoPerAccountUsage(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	member := in.register("spender", "a-good-password")
	grantAdmin(t, in, founder, operator, "dashboard")

	if err := usage.NewStore(in.db).Write(context.Background(), usage.Record{
		UserID: member.userID, ModelID: id.New(), ModelName: "model",
		InputTokens: 10, OutputTokens: 5, Credits: 1, Status: usage.StatusOK,
		StartedAt: time.Now().UnixMilli(), FinishedAt: time.Now().UnixMilli() + 40,
	}); err != nil {
		t.Fatal(err)
	}

	type rows struct {
		TopUsers []map[string]any `json:"top_users"`
		Recent   []map[string]any `json:"recent"`
	}
	delegated := in.do(http.MethodGet, "/api/admin/dashboard", nil, operator)
	if delegated.Code != http.StatusOK {
		t.Fatalf("dashboard grant: %d %s", delegated.Code, delegated.Body.String())
	}
	if got := decode[rows](t, delegated); len(got.TopUsers) != 0 || len(got.Recent) != 0 {
		t.Fatalf("dashboard grant saw per-account usage: %d top users, %d recent", len(got.TopUsers), len(got.Recent))
	}

	full := in.do(http.MethodGet, "/api/admin/dashboard", nil, founder)
	if got := decode[rows](t, full); len(got.TopUsers) == 0 || len(got.Recent) == 0 {
		t.Fatalf("usage holder lost the rankings: %d top users, %d recent", len(got.TopUsers), len(got.Recent))
	}
}

// The dashboard's aggregates are as expensive as the usage summary's, so it
// takes the same per-caller budget that summary takes.
func TestDashboardIsRateLimitedLikeTheUsageReads(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	grantAdmin(t, in, founder, operator, "dashboard")

	refused := 0
	for range 40 {
		if res := in.do(http.MethodGet, "/api/admin/dashboard", nil, operator); res.Code == http.StatusTooManyRequests {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("40 dashboard reads in a row were all answered")
	}
}

// The resources page scans every attachment on each poll. Two things bound
// that: the totals are shared for the page's own polling interval, so open
// tabs do not each scan, and a caller polling in a loop meets the usage
// budget. The first is checked by a second read inside the interval, which
// must show the first read's figures.
func TestResourcesPollingIsSharedAndLimited(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	member := in.register("uploader", "a-good-password")

	heldCount := func() int64 {
		t.Helper()
		res := in.do(http.MethodGet, "/api/admin/resources", nil, founder)
		if res.Code != http.StatusOK {
			t.Fatalf("resources: %d %s", res.Code, res.Body.String())
		}
		return decode[struct {
			Storage struct {
				HeldCount int64 `json:"held_count"`
			} `json:"storage"`
		}](t, res).Storage.HeldCount
	}
	if got := heldCount(); got != 0 {
		t.Fatalf("held before any upload = %d", got)
	}
	if _, err := conversation.NewStore(in.db).Upload(context.Background(), conversation.UploadInput{
		UserID: member.userID, Mime: "image/png", Width: 1, Height: 1, Data: []byte("not really a png"),
	}); err != nil {
		t.Fatal(err)
	}
	if got := heldCount(); got != 0 {
		t.Fatalf("a poll inside the sharing interval scanned again: held = %d", got)
	}

	refused := 0
	for range 40 {
		if res := in.do(http.MethodGet, "/api/admin/resources", nil, founder); res.Code == http.StatusTooManyRequests {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("40 resource polls in a row were all answered")
	}
}

// A sign-up review and a model probe each call the upstream provider for every
// model they touch, on the operator's key. Neither had a limit; both now refuse
// repeats beyond a small instance-wide budget, so a script cannot spend the
// key by calling them in a loop.
func TestPaidReviewAndProbeCallsAreLimited(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	grantAdmin(t, in, founder, operator, "security", "availability")

	refused := 0
	for range 12 {
		res := in.do(http.MethodPost, "/api/admin/security/review", map[string]any{"username": "probe-me"}, operator)
		if res.Code == http.StatusTooManyRequests {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("12 sign-up review calls in a row were all answered")
	}

	first := in.do(http.MethodPost, "/api/admin/health/probe", nil, operator)
	if first.Code != http.StatusOK {
		t.Fatalf("first probe: %d %s", first.Code, first.Body.String())
	}
	second := in.do(http.MethodPost, "/api/admin/health/probe", nil, operator)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("a repeat probe inside the cooldown: %d %s", second.Code, second.Body.String())
	}
}

// An import and an ordinary save must agree on the bounds of a numeric
// setting. The attachment retention bounds were checked by hand in the save
// only, so an import carried out-of-range values into storage.
func TestImportSkipsOutOfRangeAttachmentRetention(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	imported := in.do(http.MethodPost, "/api/admin/settings/import", map[string]string{
		"attachments.purge_after_days": "99999",
		"attachments.orphan_minutes":   "1",
	}, founder)
	if imported.Code != http.StatusOK {
		t.Fatalf("import: %d %s", imported.Code, imported.Body.String())
	}
	skipped := decode[map[string]any](t, imported)["skipped"].([]any)
	if len(skipped) != 2 {
		t.Fatalf("out-of-range retention was not skipped: %v", skipped)
	}

	saved := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"attachments.purge_after_days": "99999"}, founder)
	if saved.Code != http.StatusBadRequest {
		t.Fatalf("save of the same value: %d %s", saved.Code, saved.Body.String())
	}
	orphan := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"attachments.orphan_minutes": "1"}, founder)
	if orphan.Code != http.StatusBadRequest {
		t.Fatalf("save of the same orphan window: %d %s", orphan.Code, orphan.Body.String())
	}
}

// securityLog reads the security log as the super administrator sees it.
func securityLog(t *testing.T, in *instance, as *session) []struct {
	Event         string `json:"event"`
	Username      string `json:"username"`
	ActorUsername string `json:"actor_username"`
} {
	t.Helper()
	res := in.do(http.MethodGet, "/api/admin/security/events?limit=200", nil, as)
	if res.Code != http.StatusOK {
		t.Fatalf("security events: %d %s", res.Code, res.Body.String())
	}
	return decode[struct {
		Events []struct {
			Event         string `json:"event"`
			Username      string `json:"username"`
			ActorUsername string `json:"actor_username"`
		} `json:"events"`
	}](t, res).Events
}

// The request log is the one table a prune removes from, and it is where the
// only record of an administrator's action used to be. Pruning it is a super
// administrator's act when it clears everything, and every prune leaves its
// own entry in the security log, which is never pruned.
func TestLogPruneIsSuperAdminWhenItClearsEverything(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	grantAdmin(t, in, founder, operator, "logs")

	if res := in.do(http.MethodPost, "/api/admin/logs/prune", map[string]any{"days": 0}, operator); res.Code != http.StatusForbidden {
		t.Fatalf("logs-only administrator cleared the whole request log: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPost, "/api/admin/logs/prune", map[string]any{"days": 30}, operator); res.Code != http.StatusOK {
		t.Fatalf("logs-only administrator's retention prune: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPost, "/api/admin/logs/prune", map[string]any{"days": 0}, founder); res.Code != http.StatusOK {
		t.Fatalf("super administrator cleared the log: %d %s", res.Code, res.Body.String())
	}

	var pruned []string
	for _, event := range securityLog(t, in, founder) {
		if event.Event == "log_prune" && event.ActorUsername == "operator" {
			pruned = append(pruned, event.Event)
		}
	}
	if len(pruned) != 1 {
		t.Fatalf("the retention prune left %d security entries, want 1", len(pruned))
	}
}

// Resetting a password and deleting an account are the two administrative
// actions whose only record used to be the process log and the request log.
// Both are now written to the security log too.
func TestAccountResetAndDeletionAreRecordedInTheSecurityLog(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	reset := in.register("reset-me", "a-good-password")
	removed := in.register("delete-me", "a-good-password")
	grantAdmin(t, in, founder, operator, "users")

	if res := in.do(http.MethodPost, "/api/admin/users/"+reset.userID+"/password",
		map[string]any{"new_password": "replacement-password"}, operator); res.Code != http.StatusNoContent {
		t.Fatalf("password reset: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodDelete, "/api/admin/users/"+removed.userID, nil, operator); res.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", res.Code, res.Body.String())
	}

	seen := map[string]string{}
	for _, event := range securityLog(t, in, founder) {
		if event.ActorUsername == "operator" {
			seen[event.Event] = event.Username
		}
	}
	if seen["password_reset"] != "reset-me" || seen["account_deleted"] != "delete-me" {
		t.Fatalf("security log entries by the delegate = %v", seen)
	}
}

// The member picker belongs to the groups grant, which may add people to a
// group and may not read their addresses. Its search used to match the email
// address too, so a groups-only administrator could confirm an address by
// searching for it.
func TestMemberPickerDoesNotMatchEmailAddresses(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	member := in.register("member", "a-good-password")
	grantAdmin(t, in, founder, operator, "groups")
	if res := in.do(http.MethodPatch, "/api/admin/users/"+member.userID, map[string]any{"email": "private-address@example.com"}, founder); res.Code != http.StatusOK {
		t.Fatalf("set email: %d %s", res.Code, res.Body.String())
	}

	total := func(as *session, query string) int {
		t.Helper()
		res := in.do(http.MethodGet, "/api/admin/member-options?q="+query, nil, as)
		if res.Code != http.StatusOK {
			t.Fatalf("member options %q: %d %s", query, res.Code, res.Body.String())
		}
		return decode[struct {
			Total int `json:"total"`
		}](t, res).Total
	}
	if got := total(operator, "private-address"); got != 0 {
		t.Fatalf("groups-only administrator found an account by its email: %d", got)
	}
	if got := total(operator, "member"); got != 1 {
		t.Fatalf("groups-only administrator cannot find a member by name: %d", got)
	}
	if got := total(founder, "private-address"); got != 1 {
		t.Fatalf("super administrator's email search: %d", got)
	}
}
