package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

type auditRow struct {
	ID            string `json:"id"`
	ActorUsername string `json:"actor_username"`
	Method        string `json:"method"`
	Route         string `json:"route"`
	TargetID      string `json:"target_id"`
	TargetLabel   string `json:"target_label"`
	Status        int    `json:"status"`
	Via           string `json:"via"`
	Request       string `json:"request"`
	Changes       []struct {
		Field  string `json:"field"`
		From   string `json:"from"`
		To     string `json:"to"`
		Secret bool   `json:"secret"`
	} `json:"changes"`
}

func (in *instance) auditEntries(as *session, query string) []auditRow {
	in.t.Helper()
	response := in.do(http.MethodGet, "/api/admin/audit?limit=200&"+query, nil, as)
	if response.Code != http.StatusOK {
		in.t.Fatalf("read the audit trail: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Entries []auditRow `json:"entries"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		in.t.Fatal(err)
	}
	return payload.Entries
}

func (in *instance) auditEntry(as *session, entryID string) auditRow {
	in.t.Helper()
	response := in.do(http.MethodGet, "/api/admin/audit/"+entryID, nil, as)
	if response.Code != http.StatusOK {
		in.t.Fatalf("read an audit entry: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Entry auditRow `json:"entry"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		in.t.Fatal(err)
	}
	return payload.Entry
}

// The trail is complete by construction: it is written by the wrapper every
// route is mounted behind. This holds that construction to every write the
// route table has, so a route mounted some other way shows up here.
func TestEveryAdministrativeWriteIsOnTheAuditTrail(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	wildcard := regexp.MustCompile(`\{[^}]+\}`)
	writes := 0
	for _, mounted := range adminRoutes(t) {
		method, pattern, _ := strings.Cut(mounted, " ")
		if method == http.MethodGet {
			continue
		}
		writes++
		path := wildcard.ReplaceAllString(pattern, "01ARZ3NDEKTSV4RRFFQ69G5FAV")
		// An empty body is refused by most of them, which is the point: a
		// refused attempt is on the trail as well as a done one.
		in.do(method, path, map[string]any{}, founder)

		query := url.Values{"action": {method + " " + pattern}}
		if entries := in.auditEntries(founder, query.Encode()); len(entries) == 0 {
			t.Errorf("%s left nothing on the audit trail", mounted)
		} else if entries[0].ActorUsername != "founder" || entries[0].Via != "web" {
			t.Errorf("%s was recorded as %+v", mounted, entries[0])
		}
	}
	if writes < 50 {
		t.Fatalf("found only %d writes in the route table; the scanner has drifted", writes)
	}
}

func TestReadsAreAuditedOnlyWhereTheyShowSomebodyElsesData(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	member := in.register("member", "a-good-password")

	in.do(http.MethodGet, "/api/admin/dashboard", nil, founder)
	in.do(http.MethodGet, "/api/admin/users", nil, founder)
	in.do(http.MethodGet, "/api/admin/users/"+member.userID+"/conversations", nil, founder)

	entries := in.auditEntries(founder, "")
	if len(entries) != 1 {
		t.Fatalf("the trail holds %d entries after two plain reads and one sensitive one: %+v", len(entries), entries)
	}
	got := entries[0]
	if got.Route != "/api/admin/users/{id}/conversations" || got.TargetID != member.userID || got.TargetLabel != "member" {
		t.Errorf("the conversations read was recorded as %+v", got)
	}
}

func TestTheTrailMasksCredentialsAndKeepsWhatASaveReplaced(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	if response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		"site.name": "Renamed", "turnstile.secret_key": "ts-very-secret",
	}, founder); response.Code != http.StatusOK {
		t.Fatalf("save settings: %d %s", response.Code, response.Body.String())
	}
	in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "P", "kind": "openai", "base_url": "https://x.example.com/v1", "api_key": "sk-never-on-the-trail",
	}, founder)

	settingsRows := in.auditEntries(founder, url.Values{"action": {"PUT /api/admin/settings"}}.Encode())
	if len(settingsRows) != 1 {
		t.Fatalf("settings saves on the trail: %d", len(settingsRows))
	}
	saved := in.auditEntry(founder, settingsRows[0].ID)
	if strings.Contains(saved.Request, "ts-very-secret") {
		t.Errorf("the Turnstile secret reached the trail: %s", saved.Request)
	}
	var renamed, secret bool
	for _, change := range saved.Changes {
		switch change.Field {
		case "site.name":
			renamed = change.To == "Renamed" && change.From != "Renamed"
		case "turnstile.secret_key":
			secret = change.Secret && change.From == "" && change.To == ""
		}
	}
	if !renamed || !secret {
		t.Errorf("the save's changes were %+v", saved.Changes)
	}

	providerRows := in.auditEntries(founder, url.Values{"action": {"POST /api/admin/providers"}}.Encode())
	if len(providerRows) != 1 {
		t.Fatalf("provider creations on the trail: %d", len(providerRows))
	}
	if created := in.auditEntry(founder, providerRows[0].ID); strings.Contains(created.Request, "sk-never-on-the-trail") ||
		!strings.Contains(created.Request, `"api_key": "***"`) {
		t.Errorf("the provider's key was recorded as: %s", created.Request)
	}
}

func TestAnAccountEditRecordsWhatMoved(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	member := in.register("member", "a-good-password")

	if response := in.do(http.MethodPatch, "/api/admin/users/"+member.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"users"}}, founder); response.Code != http.StatusOK {
		t.Fatalf("promote: %d %s", response.Code, response.Body.String())
	}
	rows := in.auditEntries(founder, url.Values{"target_id": {member.userID}}.Encode())
	if len(rows) != 1 || rows[0].TargetLabel != "member" {
		t.Fatalf("the promotion was recorded as %+v", rows)
	}
	moved := map[string][2]string{}
	for _, change := range in.auditEntry(founder, rows[0].ID).Changes {
		moved[change.Field] = [2]string{change.From, change.To}
	}
	if moved["role"] != [2]string{"user", "admin"} || moved["admin_permissions"] != [2]string{"", "users"} {
		t.Errorf("the promotion's changes were %v", moved)
	}
}

// The trail shows what every administrator did, so reading it is a grant of
// its own, and how long it is kept is the super administrator's alone.
func TestTheTrailIsItsOwnGrantAndItsRetentionTheSuperAdministrators(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	grant := func(permissions ...string) {
		t.Helper()
		if response := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
			map[string]any{"role": "admin", "admin_permissions": permissions}, founder); response.Code != http.StatusOK {
			t.Fatalf("grant %v: %d %s", permissions, response.Code, response.Body.String())
		}
	}

	grant("logs", "settings", "security")
	if code := in.do(http.MethodGet, "/api/admin/audit", nil, operator).Code; code != http.StatusForbidden {
		t.Errorf("the trail without its grant = %d, want 403", code)
	}
	if code := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"audit.retention_days": "30"}, operator).Code; code != http.StatusForbidden {
		t.Errorf("a delegated settings grant shortening the trail = %d, want 403", code)
	}
	grant("audit")
	if code := in.do(http.MethodGet, "/api/admin/audit", nil, operator).Code; code != http.StatusOK {
		t.Errorf("the trail with its grant = %d, want 200", code)
	}

	if code := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"audit.retention_days": "1"}, founder).Code; code != http.StatusBadRequest {
		t.Errorf("a one-day retention = %d, want 400", code)
	}
	if code := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"audit.retention_days": "30"}, founder).Code; code != http.StatusOK {
		t.Errorf("a thirty-day retention = %d, want 200", code)
	}

	// The refused attempt is on the trail too, under the account that made it.
	refused := in.auditEntries(founder, url.Values{"outcome": {"failed"}, "action": {"PUT /api/admin/settings"}}.Encode())
	found := false
	for _, row := range refused {
		found = found || (row.ActorUsername == "operator" && row.Status == http.StatusForbidden)
	}
	if !found {
		t.Errorf("the operator's refused save is not on the trail: %+v", refused)
	}
}

func TestTheSweepKeepsTheTrailForItsRetention(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	in.do(http.MethodPut, "/api/admin/settings", map[string]string{"audit.retention_days": "30"}, founder)

	ctx := context.Background()
	old := time.Now().Add(-31 * 24 * time.Hour).UnixMilli()
	if _, err := in.db.Exec(ctx, `INSERT INTO admin_audit (id, at, method, route, path, status)
		VALUES (?, ?, 'POST', '/api/admin/old', '/api/admin/old', 200)`, "01ARZ3NDEKTSV4RRFFQ69G5FAV", old); err != nil {
		t.Fatal(err)
	}
	in.server.sweep(ctx)

	var count int
	if err := in.db.QueryRow(ctx, `SELECT COUNT(*) FROM admin_audit WHERE id = ?`, "01ARZ3NDEKTSV4RRFFQ69G5FAV").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("an entry past the retention survived the sweep")
	}
	if rows := in.auditEntries(founder, ""); len(rows) == 0 {
		t.Error("the sweep removed the entries inside the retention too")
	}
}
