package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func assistantFixture(t *testing.T, line string) (*instance, *session, string, <-chan map[string]any) {
	t.Helper()
	requests := make(chan map[string]any, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		messages, _ := request["messages"].([]any)
		for _, message := range messages {
			if message.(map[string]any)["role"] == "tool" {
				io.WriteString(w, `{"choices":[{"message":{"content":"Verified the result."},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5}}`)
				return
			}
		}
		args, _ := json.Marshal(map[string]string{"line": line})
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "Inspect first.", "tool_calls": []any{map[string]any{"id": "call-1", "type": "function", "function": map[string]any{"name": "run_admin_command", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 4},
		})
	}))
	t.Cleanup(upstream.Close)
	in := newInstance(t)
	root := in.register("founder", "a-good-password")
	provider := decode[struct{ Provider struct{ ID string } }](t, in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Assistant upstream", "kind": "openai", "base_url": upstream.URL + "/v1", "api_key": "sk-assistant-test",
	}, root))
	model := decode[struct{ Model struct{ ID string } }](t, in.do(http.MethodPost, "/api/admin/models", map[string]any{
		"provider_id": provider.Provider.ID, "model_id": "assistant", "display_name": "Assistant", "enabled": true,
		"supports_system_prompt": true, "supports_streaming": false,
	}, root))
	return in, root, model.Model.ID, requests
}

func assistantInput(model string) map[string]any {
	return map[string]any{"model_id": model, "mode": "full", "messages": []map[string]any{{"role": "user", "content": "Inspect the instance"}}}
}

func TestAssistantUsesRealToolsAndAccountsForEveryProviderCall(t *testing.T) {
	in, root, model, upstream := assistantFixture(t, "user list")
	request := assistantInput(model)
	response := in.do(http.MethodPost, "/api/admin/assistant", request, root)
	if response.Code != http.StatusOK {
		t.Fatalf("assistant=%d %s", response.Code, response.Body.String())
	}
	var result struct {
		Content string           `json:"content"`
		Calls   []map[string]any `json:"tool_calls"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Calls) != 1 || result.Calls[0]["read_only"] != true {
		t.Fatalf("proposal=%s", response.Body.String())
	}
	if request := <-upstream; !strings.Contains(fmt.Sprint(request["messages"]), "administration assistant") || request["tools"] == nil {
		t.Fatalf("upstream request=%v", request)
	}
	out, verdict := consoleRun(t, in, root, "user list")
	if !verdict.OK {
		t.Fatalf("tool refused: %s", out)
	}
	request["messages"] = []map[string]any{
		{"role": "user", "content": "Inspect the instance"},
		{"role": "assistant", "content": result.Content, "tool_calls": result.Calls},
		{"role": "tool", "tool_call_id": "call-1", "content": out},
	}
	response = in.do(http.MethodPost, "/api/admin/assistant", request, root)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Verified the result") {
		t.Fatalf("continuation=%d %s", response.Code, response.Body.String())
	}
	var turns, tokens, conversations int
	ctx := context.Background()
	if err := in.db.QueryRow(ctx, `SELECT COUNT(*), SUM(input_tokens + output_tokens) FROM usage_records WHERE user_id = ?`, root.userID).Scan(&turns, &tokens); err != nil {
		t.Fatal(err)
	}
	if turns != 2 || tokens != 39 {
		t.Fatalf("usage turns=%d tokens=%d", turns, tokens)
	}
	if err := in.db.QueryRow(ctx, `SELECT COUNT(*) FROM conversations`).Scan(&conversations); err != nil || conversations != 0 {
		t.Fatalf("ordinary conversations=%d err=%v", conversations, err)
	}
}

func TestAssistantCannotOfferToolsOutsideADelegatedAdministratorsGrants(t *testing.T) {
	in, root, model, upstream := assistantFixture(t, "provider delete example --yes")
	limited := in.register("operator", "a-good-password")
	delegate(t, in, root, limited, "users")
	response := in.do(http.MethodPost, "/api/admin/assistant", assistantInput(model), limited)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "assistant_command_invalid") {
		t.Fatalf("ungranted tool=%d %s", response.Code, response.Body.String())
	}
	request := <-upstream
	encoded, _ := json.Marshal(request["messages"])
	if strings.Contains(string(encoded), `\"name\":\"provider delete\"`) {
		t.Fatalf("ungranted catalogue leaked: %s", encoded)
	}
}

func TestFullAccessAssistantStillRequiresTheAdministratorsOwnVerification(t *testing.T) {
	in, root, model, _ := assistantFixture(t, "user list")
	in.enrol(root)
	response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"security.two_factor_backoffice_mode": "visit"}, root)
	if response.Code != http.StatusOK {
		t.Fatalf("set verification: %d %s", response.Code, response.Body.String())
	}
	in.do(http.MethodPost, "/api/profile/two-factor/backoffice/leave", nil, root)
	response = in.do(http.MethodPost, "/api/admin/assistant", assistantInput(model), root)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "two_factor_backoffice_verify") {
		t.Fatalf("full mode bypassed verification: %d %s", response.Code, response.Body.String())
	}
	_, verdict := consoleRun(t, in, root, "user list")
	if verdict.OK || verdict.Code != "two_factor_backoffice_verify" {
		t.Fatalf("tool bypassed verification: %+v", verdict)
	}
}

func TestAssistantProposesAWriteWithoutExecutingItAndTheRealCommandAppliesIt(t *testing.T) {
	in, root, model, _ := assistantFixture(t, "user edit target --nickname Updated")
	target := in.register("target", "a-good-password")
	response := in.do(http.MethodPost, "/api/admin/assistant", assistantInput(model), root)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"read_only":true`) {
		t.Fatalf("write proposal=%d %s", response.Code, response.Body.String())
	}
	var nickname string
	ctx := context.Background()
	if err := in.db.QueryRow(ctx, `SELECT nickname FROM users WHERE id = ?`, target.userID).Scan(&nickname); err != nil {
		t.Fatal(err)
	}
	if nickname == "Updated" {
		t.Fatal("model call executed a write before browser approval policy")
	}
	out, verdict := consoleRun(t, in, root, "user edit target --nickname Updated")
	if !verdict.OK {
		t.Fatalf("write failed: %s", out)
	}
	if err := in.db.QueryRow(ctx, `SELECT nickname FROM users WHERE id = ?`, target.userID).Scan(&nickname); err != nil || nickname != "Updated" {
		t.Fatalf("approved write nickname=%q err=%v", nickname, err)
	}
}
