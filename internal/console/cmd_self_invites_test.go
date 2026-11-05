package console

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func TestSelfInviteCommands(t *testing.T) {
	actor := user.User{ID: id.New(), Username: "alice", Role: user.RoleUser}

	type callRecord struct {
		Method string
		Path   string
		Body   any
	}
	var lastCall callRecord
	var regenerated bool

	c := New(Options{Dispatch: func(_ context.Context, _ user.User, method, path string, body any) (Response, error) {
		lastCall = callRecord{Method: method, Path: path, Body: body}
		switch {
		case method == "GET" && path == "/api/profile/invites":
			code := "AB12CD34"
			if regenerated {
				code = "ZZ99YY88"
			}
			// counted=4, reward_every=3: the third invite (bob) landed on a
			// milestone and paid 2 cards, dana's counted but did not, carl
			// never counted at all. next_reward_in=2 is 3-(4%3), the same
			// arithmetic invite.nextRewardIn uses server-side.
			return Response{Status: 200, Body: []byte(`{"enabled":true,"code":"` + code + `","limit":10,"used":3,
				"counted":4,"reward_every":3,"reward_cards":2,"reward_card_days":30,"next_reward_in":2,"invitees":[
					{"nickname":"Bob","username":"bob","created_at":1700000000000,"counted":true,"reward_cards":2,"reward_skipped":""},
					{"nickname":"Dana","username":"dana","created_at":1700000500000,"counted":true,"reward_cards":0,"reward_skipped":""},
					{"nickname":"","username":"carl","created_at":1700001000000,"counted":false,"reward_cards":0,"reward_skipped":"limit"}
				]}`)}, nil
		case method == "POST" && path == "/api/profile/invites/regenerate":
			regenerated = true
			return Response{Status: 200, Body: []byte(`{"enabled":true,"code":"ZZ99YY88","limit":10,"used":0,
				"counted":0,"reward_every":3,"reward_cards":2,"reward_card_days":30,"next_reward_in":0,"invitees":[]}`)}, nil
		case method == "POST" && path == "/api/profile/invites/claim":
			m, _ := body.(map[string]any)
			if m["code"] == "BADCODE" {
				return Response{Status: 400, Body: []byte(`{"error":{"code":"invite_invalid","message":"That invite code is not valid."}}`)}, nil
			}
			return Response{Status: 200, Body: []byte(`{"group_id":"01GRP","group_name":"Trial","days":5,"expires_at":1700100000000}`)}, nil
		default:
			return Response{Status: 404, Body: []byte(`{"error":{"code":"not_found","message":"Not found."}}`)}, nil
		}
	}})

	run := func(line string) (Result, string) {
		var out bytes.Buffer
		s := &Session{Actor: actor, Transport: "web", Lang: "en", Width: 120}
		return c.Execute(context.Background(), s, &out, line), out.String()
	}

	t.Run("me invite shows the code hyphenated, the reward cadence and the invitee table", func(t *testing.T) {
		result, out := run("me invite")
		if !result.OK {
			t.Fatalf("me invite failed: %s", out)
		}
		if lastCall.Method != "GET" || lastCall.Path != "/api/profile/invites" {
			t.Errorf("unexpected call: %v", lastCall)
		}
		if !strings.Contains(out, "AB12-CD34") {
			t.Errorf("expected the hyphenated code:\n%s", out)
		}
		for _, want := range []string{"counted:", "4", "reward_every:", "3", "next_reward_in:", "2"} {
			if !strings.Contains(out, want) {
				t.Errorf("expected %q in the reward-cadence fields:\n%s", want, out)
			}
		}
		if !strings.Contains(out, "bob") || !strings.Contains(out, "carl") || !strings.Contains(out, "dana") {
			t.Errorf("expected all three invitees listed:\n%s", out)
		}
		if !strings.Contains(out, "+2 cards") {
			t.Errorf("expected bob's milestone reward shown as +2 cards:\n%s", out)
		}
		if !strings.Contains(out, "counted") {
			t.Errorf("expected dana's counted-but-no-reward row shown:\n%s", out)
		}
		if !strings.Contains(out, "limit") {
			t.Errorf("expected carl's skip reason shown:\n%s", out)
		}
	})

	t.Run("regenerate replaces the code", func(t *testing.T) {
		result, out := run("me invite regenerate --yes")
		if !result.OK {
			t.Fatalf("me invite regenerate failed: %s", out)
		}
		if lastCall.Method != "POST" || lastCall.Path != "/api/profile/invites/regenerate" {
			t.Errorf("unexpected call: %v", lastCall)
		}
		if !strings.Contains(out, "ZZ99-YY88") {
			t.Errorf("expected the new code hyphenated:\n%s", out)
		}
	})

	t.Run("claim sends the typed code and prints the group it joined", func(t *testing.T) {
		result, out := run("me invite claim PARTNERX")
		if !result.OK {
			t.Fatalf("me invite claim failed: %s", out)
		}
		if lastCall.Method != "POST" || lastCall.Path != "/api/profile/invites/claim" {
			t.Errorf("unexpected call: %v", lastCall)
		}
		body, _ := lastCall.Body.(map[string]any)
		if body["code"] != "PARTNERX" {
			t.Errorf("expected the typed code sent verbatim, got %v", body)
		}
		for _, want := range []string{"Trial", "5"} {
			if !strings.Contains(out, want) {
				t.Errorf("expected %q in the claim result:\n%s", want, out)
			}
		}
	})

	t.Run("claim requires a code argument", func(t *testing.T) {
		result, _ := run("me invite claim")
		if result.OK {
			t.Fatal("me invite claim with no argument should be refused")
		}
	})

	t.Run("claim surfaces the server's refusal for a bad code", func(t *testing.T) {
		result, out := run("me invite claim BADCODE")
		if result.OK {
			t.Fatal("an invalid code should not succeed")
		}
		if result.Code != "invite_invalid" {
			t.Errorf("want invite_invalid, got %q (output: %q)", result.Code, out)
		}
	})

}
