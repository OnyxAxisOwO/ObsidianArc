package console

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Every invite command round-trips through the same generic dispatcher the
// rest of this package's command tests use — a stand-in for the real
// admin.Handlers that only checks what request each command actually sent
// and what it printed from the response, never the storage layer
// internal/invite already tests on its own.
func TestInviteCommands(t *testing.T) {
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
	groupID := id.New()
	codeID := id.New()

	type callRecord struct {
		Method string
		Path   string
		Body   any
	}
	var lastCall callRecord

	c := New(Options{Dispatch: func(_ context.Context, _ user.User, method, path string, body any) (Response, error) {
		lastCall = callRecord{Method: method, Path: path, Body: body}
		switch {
		case method == "GET" && strings.HasPrefix(path, "/api/admin/invites?") && strings.Contains(path, "q=PARTNERX"):
			// Resolution lookups (invite revoke/uses given a code rather
			// than an id) and the "search" flag share this same shape.
			return Response{Status: 200, Body: []byte(`{"codes":[
				{"id":"` + codeID + `","code":"PARTNERX","owner_id":"","owner_username":"","owner_nickname":"",
				 "kind":"partner","name":"Acme Corp","allow_existing":true,
				 "group_id":"","group_name":"","group_days":0,"group_days_max":0,"max_uses":0,"uses":3,"claims":2,
				 "expires_at":0,"revoked_at":0,"note":"aff","created_by":"root","created_at":1700000000000,"status":"active"}
			],"total":1}`)}, nil
		case method == "GET" && path == "/api/admin/invites?kind=admin&limit=50&status=active":
			return Response{Status: 200, Body: []byte(`{"codes":[
				{"id":"` + codeID + `","code":"AB12CD34","owner_id":"","owner_username":"","owner_nickname":"",
				 "kind":"batch","name":"","allow_existing":false,
				 "group_id":"","group_name":"","group_days":0,"group_days_max":0,"max_uses":10,"uses":2,"claims":0,
				 "expires_at":0,"revoked_at":0,"note":"","created_by":"root","created_at":1700000000000,"status":"active"}
			],"total":1}`)}, nil
		case method == "POST" && path == "/api/admin/invites":
			return Response{Status: 201, Body: []byte(`{"codes":[
				{"id":"` + codeID + `","code":"AB12CD34","owner_id":"","owner_username":"","owner_nickname":"",
				 "kind":"batch","name":"","allow_existing":false,
				 "group_id":"` + groupID + `","group_name":"Trial","group_days":3,"group_days_max":7,"max_uses":0,"uses":0,"claims":0,
				 "expires_at":0,"revoked_at":0,"note":"partner","created_by":"root","created_at":1700000000000,"status":"active"}
			]}`)}, nil
		case method == "DELETE" && path == "/api/admin/invites/"+codeID:
			return Response{Status: 200, Body: []byte(`{"code":{"id":"` + codeID + `","code":"PARTNERX","owner_id":"","owner_username":"",
				"owner_nickname":"","kind":"partner","name":"Acme Corp","allow_existing":true,
				"group_id":"","group_name":"","group_days":0,"group_days_max":0,"max_uses":0,"uses":3,"claims":2,
				"expires_at":0,"revoked_at":1700005000000,"note":"aff","created_by":"root","created_at":1700000000000,"status":"revoked"}}`)}, nil
		case method == "GET" && path == "/api/admin/invites/"+codeID+"/uses":
			return Response{Status: 200, Body: []byte(`{"uses":[
				{"user_id":"01U1","username":"alice","nickname":"Alice","via":"register","group_days":5,"created_at":1700001000000,"rewarded_at":1700002000000,"reward_cards":2,"reward_skipped":""},
				{"user_id":"01U2","username":"bob","nickname":"","via":"register","group_days":0,"created_at":1700003000000,"rewarded_at":1700003500000,"reward_cards":0,"reward_skipped":"same_ip"},
				{"user_id":"01U3","username":"carl","nickname":"Carl","via":"claim","group_days":5,"created_at":1700004000000,"rewarded_at":0,"reward_cards":0,"reward_skipped":""}
			]}`)}, nil
		case method == "GET" && path == "/api/admin/references":
			return Response{Status: 200, Body: []byte(`{"groups":[{"id":"` + groupID + `","name":"Trial"}]}`)}, nil
		default:
			return Response{Status: 404, Body: []byte(`{"error":{"code":"not_found","message":"Not found."}}`)}, nil
		}
	}})

	run := func(line string) (Result, string) {
		var out bytes.Buffer
		s := &Session{Actor: actor, Transport: "web", Lang: "en", Width: 120}
		return c.Execute(context.Background(), s, &out, line), out.String()
	}

	t.Run("list applies filters and hyphenates the code", func(t *testing.T) {
		result, out := run("invite list --kind admin --status active")
		if !result.OK {
			t.Fatalf("invite list failed: %s", out)
		}
		if lastCall.Method != "GET" || lastCall.Path != "/api/admin/invites?kind=admin&limit=50&status=active" {
			t.Errorf("unexpected call: %v", lastCall)
		}
		if !strings.Contains(out, "AB12-CD34") {
			t.Errorf("expected the generated code hyphenated as AB12-CD34:\n%s", out)
		}
		if !strings.Contains(out, "batch") {
			t.Errorf("expected the kind column:\n%s", out)
		}
		if !strings.Contains(out, "1 total") {
			t.Errorf("expected a total line:\n%s", out)
		}
	})

	t.Run("create rejects both --days and --expires", func(t *testing.T) {
		result, out := run("invite create --days 7 --expires 2026-01-01")
		if result.OK {
			t.Fatal("--days and --expires together should be refused")
		}
		if !strings.Contains(out, "at most one") {
			t.Errorf("expected a mutual-exclusion error, got: %s", out)
		}
	})

	t.Run("create resolves --group and parses a --group-days range", func(t *testing.T) {
		result, out := run("invite create --code PARTNERX --uses 0 --group Trial --group-days 3-7 --note partner")
		if !result.OK {
			t.Fatalf("invite create failed: %s", out)
		}
		if lastCall.Method != "POST" || lastCall.Path != "/api/admin/invites" {
			t.Errorf("unexpected call: %v", lastCall)
		}
		payload, _ := json.Marshal(lastCall.Body)
		body := string(payload)
		for _, want := range []string{`"code":"PARTNERX"`, `"max_uses":0`, `"group_id":"` + groupID + `"`, `"group_days":3`, `"group_days_max":7`, `"note":"partner"`} {
			if !strings.Contains(body, want) {
				t.Errorf("body %s missing %s", body, want)
			}
		}
		if !strings.Contains(out, "AB12-CD34") {
			t.Errorf("expected the minted code in the output:\n%s", out)
		}
	})

	t.Run("create --partner sends a partner kind and name, and defaults allow_existing to the server", func(t *testing.T) {
		result, out := run("invite create --partner 'Acme Corp' --code PARTNERX --uses 0 --group Trial --group-days 3-7")
		if !result.OK {
			t.Fatalf("invite create --partner failed: %s", out)
		}
		payload, _ := json.Marshal(lastCall.Body)
		body := string(payload)
		for _, want := range []string{`"kind":"partner"`, `"name":"Acme Corp"`, `"code":"PARTNERX"`} {
			if !strings.Contains(body, want) {
				t.Errorf("body %s missing %s", body, want)
			}
		}
		// Not mentioned at all, so the server picks the partner default
		// (true) — see invite.Create. A client-side default here would be
		// a second place that decision could drift from the API's own.
		if strings.Contains(body, `"allow_existing"`) {
			t.Errorf("expected allow_existing left for the server to default, got: %s", body)
		}
	})

	t.Run("create --no-existing explicitly turns allow_existing off", func(t *testing.T) {
		run("invite create --partner 'Acme Corp' --code PARTNERX --uses 0 --group Trial --group-days 3-7 --no-existing")
		payload, _ := json.Marshal(lastCall.Body)
		if !strings.Contains(string(payload), `"allow_existing":false`) {
			t.Errorf("expected --no-existing to send allow_existing:false, got: %s", payload)
		}
	})

	t.Run("create translates --days into an absolute expiry", func(t *testing.T) {
		run("invite create --days 30")
		payload, _ := json.Marshal(lastCall.Body)
		if !strings.Contains(string(payload), `"expires_at":`) {
			t.Errorf("expected expires_at to be set from --days: %s", payload)
		}
		if strings.Contains(string(payload), `"expires_at":0`) {
			t.Errorf("--days 30 should not resolve to an epoch of 0: %s", payload)
		}
	})

	t.Run("revoke resolves a code to its id", func(t *testing.T) {
		result, out := run("invite revoke PARTNERX --yes")
		if !result.OK {
			t.Fatalf("invite revoke failed: %s", out)
		}
		if lastCall.Method != "DELETE" || lastCall.Path != "/api/admin/invites/"+codeID {
			t.Errorf("expected the code resolved to its id before DELETE, got: %v", lastCall)
		}
		if !strings.Contains(out, "revoked") {
			t.Errorf("expected the revoked status in the output:\n%s", out)
		}
	})

	t.Run("revoke accepts a hyphenated or lower-case code the same way", func(t *testing.T) {
		result, out := run("invite revoke partner-x --yes")
		if !result.OK {
			t.Fatalf("invite revoke with a folded ref failed: %s", out)
		}
		if lastCall.Path != "/api/admin/invites/"+codeID {
			t.Errorf("expected the same code id regardless of casing/hyphens, got: %v", lastCall)
		}
	})

	t.Run("uses shows the reward outcome and the via source per invitee", func(t *testing.T) {
		result, out := run("invite uses PARTNERX")
		if !result.OK {
			t.Fatalf("invite uses failed: %s", out)
		}
		if !strings.Contains(out, "+2 cards") {
			t.Errorf("expected alice's granted reward shown as +2 cards:\n%s", out)
		}
		if !strings.Contains(out, "same_ip") {
			t.Errorf("expected bob's skip reason shown:\n%s", out)
		}
		if !strings.Contains(out, "carl") {
			t.Errorf("expected carl's claim row listed:\n%s", out)
		}
		registerCount := strings.Count(out, "register")
		if registerCount < 2 {
			t.Errorf("expected via=register for both alice and bob:\n%s", out)
		}
		if !strings.Contains(out, "claim") {
			t.Errorf("expected via=claim for carl's row:\n%s", out)
		}
	})

}

// A 26-character custom code is spelled in the ULID alphabet, so it looks
// exactly like a row id. It has to be looked up as a code first, or revoking
// it sends its own text to the server as an id no row has.
func TestAnIDShapedCustomCodeIsResolvedAsACode(t *testing.T) {
	const code = "AAAAAAAAAAAAAAAAAAAAAAAAAA"
	rowID := id.New()
	var deleted string
	c := New(Options{Dispatch: func(_ context.Context, _ user.User, method, path string, _ any) (Response, error) {
		switch {
		case method == "GET" && strings.HasPrefix(path, "/api/admin/invites?"):
			return Response{Status: 200, Body: []byte(`{"codes":[{"id":"` + rowID + `","code":"` + code + `",` +
				`"max_uses":0,"uses":0,"status":"active"}],"total":1}`)}, nil
		case method == "DELETE":
			deleted = path
			return Response{Status: 200, Body: []byte(`{"code":{"id":"` + rowID + `","code":"` + code + `","status":"revoked"}}`)}, nil
		}
		return Response{Status: 404, Body: []byte(`{"error":{"code":"not_found","message":"no"}}`)}, nil
	}})
	var out bytes.Buffer
	s := &Session{Actor: user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}, Transport: "web", Lang: "en", Width: 120}
	if result := c.Execute(context.Background(), s, &out, "invite revoke "+code+" --yes"); !result.OK {
		t.Fatalf("revoke: %s", out.String())
	}
	if deleted != "/api/admin/invites/"+rowID {
		t.Fatalf("deleted %q, want the row's id", deleted)
	}
}
