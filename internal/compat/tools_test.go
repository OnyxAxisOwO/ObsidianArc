package compat

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

// The endpoint used to accept `tools` and drop them on the floor, and to
// refuse a `tool` message outright. Both together meant no agent — Claude
// Code, Codex, anything running a tool loop — could get past its first turn:
// it offered its tools, got prose back, and had nowhere to send a result. The
// tests below are that loop, one step at a time.

// One turn of an agent's loop as its client writes it.
const toolsRequest = `{"model":"%MODEL%","stream":false,` +
	`"tools":[{"type":"function","function":{"name":"read_file",` +
	`"description":"Read a file","parameters":{"type":"object",` +
	`"properties":{"path":{"type":"string"}},"required":["path"]}}}],` +
	`"tool_choice":"auto",` +
	`"messages":[{"role":"user","content":"what is in main.go"}]}`

func toolsBody(modelName string) string {
	return strings.ReplaceAll(toolsRequest, "%MODEL%", modelName)
}

// What a provider answers with when the model wants a tool run.
const toolAnswer = `{"choices":[{"message":{"content":null,"tool_calls":[` +
	`{"id":"call_provider_side_id","type":"function",` +
	`"function":{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}}]},` +
	`"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":40,"completion_tokens":9}}`

func TestToolsReachTheProvider(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(toolAnswer)

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, toolsBody(f.model.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	tools, _ := f.upstream.received()["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("the provider was sent %v", f.upstream.received()["tools"])
	}
	entry, _ := tools[0].(map[string]any)
	function, _ := entry["function"].(map[string]any)
	if function["name"] != "read_file" {
		t.Errorf("tool name = %v", function["name"])
	}
	schema, _ := function["parameters"].(map[string]any)
	required, _ := schema["required"].([]any)
	if len(required) != 1 || required[0] != "path" {
		t.Errorf("the caller's schema did not survive: %v", function["parameters"])
	}
}

func TestBufferedAnswerCarriesToolCalls(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(toolAnswer)

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, toolsBody(f.model.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	body := decodeJSON(t, w)
	choices, _ := body["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices = %v", body["choices"])
	}
	first, _ := choices[0].(map[string]any)
	if first["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", first["finish_reason"])
	}

	message, _ := first["message"].(map[string]any)
	// Present and null, not missing: a client decoding into a non-optional
	// field fails on a key that is absent.
	content, present := message["content"]
	if !present || content != nil {
		t.Errorf("content = %v (present %v), want null", content, present)
	}

	calls, _ := message["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("tool_calls = %v", message["tool_calls"])
	}
	call, _ := calls[0].(map[string]any)
	if call["type"] != "function" {
		t.Errorf("call type = %v", call["type"])
	}
	if id, _ := call["id"].(string); id == "" {
		t.Error("the call has no id to answer it by")
	}
	made, _ := call["function"].(map[string]any)
	if made["name"] != "read_file" {
		t.Errorf("call name = %v", made["name"])
	}
	// Text, not an object, and exactly what the model wrote.
	if made["arguments"] != `{"path":"main.go"}` {
		t.Errorf("arguments = %v", made["arguments"])
	}
	// A finished message carries no index; that field belongs to a stream.
	if _, present := call["index"]; present {
		t.Error("a buffered call carried a stream chunk's index")
	}
}

// The same rule as the model name, the finish reason and the error text: a
// provider's own spelling stays inside. `toolu_…` would name the family that
// answered as plainly as a header would.
func TestProviderToolCallIdentifiersAreNotForwarded(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(toolAnswer)

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, toolsBody(f.model.ID))
	if strings.Contains(w.Body.String(), "call_provider_side_id") {
		t.Errorf("the provider's own call id was forwarded: %s", w.Body.String())
	}
}

// The whole point of minting an id: the client sends it back, and it has to
// come out the far side against the call it answers.
func TestAToolResultCompletesTheLoop(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(toolAnswer)

	// Turn one: the model asks for a tool.
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, toolsBody(f.model.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("first turn: status = %d: %s", w.Code, w.Body.String())
	}
	body := decodeJSON(t, w)
	choices, _ := body["choices"].([]any)
	first, _ := choices[0].(map[string]any)
	message, _ := first["message"].(map[string]any)
	calls, _ := message["tool_calls"].([]any)
	call, _ := calls[0].(map[string]any)
	id, _ := call["id"].(string)

	// Turn two: the client replays the call and answers it, the way every
	// agent's loop does.
	f.upstream.reply(answer)
	replay := `{"model":"` + f.model.ID + `","stream":false,"messages":[` +
		`{"role":"user","content":"what is in main.go"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"` + id + `",` +
		`"type":"function","function":{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}}]},` +
		`{"role":"tool","tool_call_id":"` + id + `","content":"package main"}]}`

	w = f.do(t, http.MethodPost, "/v1/chat/completions", f.token, replay)
	if w.Code != http.StatusOK {
		t.Fatalf("second turn: status = %d: %s", w.Code, w.Body.String())
	}

	sent, _ := f.upstream.received()["messages"].([]any)
	if len(sent) != 3 {
		t.Fatalf("the provider was sent %v, want three messages", f.upstream.received()["messages"])
	}
	assistant, _ := sent[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Fatalf("second message role = %v", assistant["role"])
	}
	replayed, _ := assistant["tool_calls"].([]any)
	if len(replayed) != 1 {
		t.Fatalf("the replayed call did not reach the provider: %v", assistant)
	}
	made, _ := replayed[0].(map[string]any)
	if made["id"] != id {
		t.Errorf("the call reached the provider as %v, want %q", made["id"], id)
	}

	result, _ := sent[2].(map[string]any)
	if result["role"] != "tool" || result["tool_call_id"] != id {
		t.Errorf("the result reached the provider as %v", result)
	}
	if result["content"] != "package main" {
		t.Errorf("result content = %v", result["content"])
	}
}

// Both protocols refuse a result that names no call. Saying so here is more
// use than letting the provider say it in its own words.
func TestAToolMessageMustNameTheCallItAnswers(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","stream":false,"messages":[` +
		`{"role":"user","content":"hi"},{"role":"tool","content":"orphaned"}]}`
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "messages" {
		t.Errorf("code = %q, want messages", got)
	}
}

func TestToolChoiceIsTranslated(t *testing.T) {
	f := newFixture(t)

	cases := []struct {
		sent string
		want any
	}{
		{`"auto"`, nil},
		{`"none"`, "none"},
		{`"required"`, "required"},
		{`{"type":"function","function":{"name":"read_file"}}`, "named"},
	}

	for _, tc := range cases {
		f.upstream.reply(answer)
		body := strings.Replace(toolsBody(f.model.ID), `"tool_choice":"auto"`,
			`"tool_choice":`+tc.sent, 1)
		w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
		if w.Code != http.StatusOK {
			t.Fatalf("tool_choice %s: status = %d: %s", tc.sent, w.Code, w.Body.String())
		}

		got := f.upstream.received()["tool_choice"]
		switch tc.want {
		case nil:
			if got != nil {
				t.Errorf("tool_choice %s went out as %v, want nothing", tc.sent, got)
			}
		case "named":
			object, _ := got.(map[string]any)
			function, _ := object["function"].(map[string]any)
			if function["name"] != "read_file" {
				t.Errorf("tool_choice %s went out as %v", tc.sent, got)
			}
		default:
			if got != tc.want {
				t.Errorf("tool_choice %s went out as %v, want %v", tc.sent, got, tc.want)
			}
		}
	}
}

func TestUnknownToolChoiceIsRefused(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := strings.Replace(toolsBody(f.model.ID), `"tool_choice":"auto"`,
		`"tool_choice":"whenever"`, 1)
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "tool_choice" {
		t.Errorf("code = %q, want tool_choice", got)
	}
}

// A tool this server cannot run must not be answered as though it had been
// offered: the model would call something that does not exist.
func TestAHostedToolTypeIsRefused(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","stream":false,` +
		`"tools":[{"type":"web_search_preview"}],` +
		`"messages":[{"role":"user","content":"hi"}]}`
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "tools" {
		t.Errorf("code = %q, want tools", got)
	}
}

// --- the streamed shape ---------------------------------------------------------

func TestStreamedAnswerCarriesToolCalls(t *testing.T) {
	f := newFixture(t)
	f.upstream.stream(
		`{"choices":[{"delta":{"role":"assistant"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_upstream","type":"function",`+
			`"function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"main.go\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":40,"completion_tokens":9}}`,
	)

	body := strings.Replace(toolsBody(f.model.ID), `"stream":false`, `"stream":true`, 1)
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	raw := w.Body.String()
	// Assembled from the fragments and sent once, whole: half an arguments
	// object is nothing a client can act on.
	if !strings.Contains(raw, `{\"path\":\"main.go\"}`) {
		t.Errorf("the assembled arguments never went out: %s", raw)
	}
	// A streamed call is keyed by its position, which is how a client knows
	// which call a chunk belongs to.
	if !strings.Contains(raw, `"index":0`) {
		t.Errorf("the streamed call carried no index: %s", raw)
	}
	if !strings.Contains(raw, `"finish_reason":"tool_calls"`) {
		t.Errorf("the stream did not finish with tool_calls: %s", raw)
	}
	if strings.Contains(raw, "call_upstream") {
		t.Errorf("the provider's own call id was forwarded: %s", raw)
	}
	if !strings.Contains(raw, "[DONE]") {
		t.Errorf("the stream did not end: %s", raw)
	}
}

// An SDK replays the assistant message it was handed, whole. OpenAI's own
// puts `refusal` and `annotations` on one, and a decoder that refuses a field
// it has not heard of ends the loop on its second step — which is where the
// official client stopped.
func TestAReplayedAssistantMessageMayCarryFieldsWeDoNotRead(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","stream":false,"parallel_tool_calls":false,` +
		`"messages":[{"role":"user","content":"hi"},` +
		`{"role":"assistant","content":null,"refusal":null,"annotations":[],` +
		`"tool_calls":[{"id":"call_a","type":"function","index":0,` +
		`"function":{"name":"read_file","arguments":"{}"}}]},` +
		`{"role":"tool","tool_call_id":"call_a","content":"package main"}]}`

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	// Ignored, not forwarded: leniency is about what may arrive, and
	// completionRequest still decides what leaves.
	if _, present := f.upstream.received()["parallel_tool_calls"]; present {
		t.Error("an unread field reached the provider")
	}
	if _, present := f.upstream.received()["annotations"]; present {
		t.Error("an unread field reached the provider")
	}
}

// `stream` is absent from an ordinary call by every client library there is,
// and the protocol says absent means false. Answering one with an event
// stream hands it a body it is not parsing, which is where the official SDK
// stopped.
func TestStreamDefaultsToOffWhenTheFieldIsAbsent(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","messages":[{"role":"user","content":"hi"}]}`
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if strings.HasPrefix(strings.TrimSpace(w.Body.String()), "data:") {
		t.Errorf("a request that did not ask for a stream got one: %s", w.Body.String())
	}
	if object := decodeJSON(t, w)["object"]; object != "chat.completion" {
		t.Errorf("object = %v, want chat.completion", object)
	}
}

// A provider that ends a turn with calls but calls it "stop" — several
// compatible servers do — must not stop the client's loop.
func TestToolCallsSetTheFinishReasonWhateverTheProviderSaid(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(`{"choices":[{"message":{"content":null,"tool_calls":[` +
		`{"id":"c","type":"function","function":{"name":"read_file","arguments":"{}"}}]},` +
		`"finish_reason":"stop"}]}`)

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, toolsBody(f.model.ID))
	body := decodeJSON(t, w)
	choices, _ := body["choices"].([]any)
	first, _ := choices[0].(map[string]any)
	if first["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", first["finish_reason"])
	}
}

// A model an administrator marked as not streaming still has to answer a
// streaming client, and its calls have to reach that client as chunks.
func TestToolCallsReachAStreamingClientFromANonStreamingModel(t *testing.T) {
	f := newFixture(t)
	off := false
	if _, err := f.models.Update(context.Background(), f.model.ID,
		model.Update{SupportsStreaming: &off}); err != nil {
		t.Fatal(err)
	}
	f.upstream.reply(toolAnswer)

	body := strings.Replace(toolsBody(f.model.ID), `"stream":false`, `"stream":true`, 1)
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	raw := w.Body.String()
	if !strings.Contains(raw, `"name":"read_file"`) {
		t.Errorf("the call never reached the client: %s", raw)
	}
	if !strings.Contains(raw, `"finish_reason":"tool_calls"`) {
		t.Errorf("the stream did not finish with tool_calls: %s", raw)
	}
}
