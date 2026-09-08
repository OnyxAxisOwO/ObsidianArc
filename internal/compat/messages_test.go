package compat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The Anthropic surface. Its own protocol end to end: blocks in, blocks out,
// named events on the stream, and an error envelope its clients parse before
// they have anything else to go on.

func messagesBody(modelName string) string {
	return `{"model":"` + modelName + `","max_tokens":1024,"stream":false,` +
		`"system":"You are helpful.",` +
		`"tools":[{"name":"read_file","description":"Read a file",` +
		`"input_schema":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}],` +
		`"messages":[{"role":"user","content":"what is in main.go?"}]}`
}

func TestMessagesTranslatesToolsOnTheWayOut(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(toolAnswer)

	w := f.do(t, http.MethodPost, "/v1/messages", f.token, messagesBody(f.model.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	// The provider is OpenAI-shaped whatever the caller spoke, so the schema
	// has to arrive under the other protocol's field name.
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
	if _, ok := schema["properties"]; !ok {
		t.Errorf("input_schema did not become parameters: %v", function["parameters"])
	}

	// The system prompt is a message on that side and a field on this one.
	messages, _ := f.upstream.received()["messages"].([]any)
	first, _ := messages[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "You are helpful." {
		t.Errorf("system prompt reached the provider as %v", first)
	}
}

func TestMessagesAnswersWithContentBlocks(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(toolAnswer)

	w := f.do(t, http.MethodPost, "/v1/messages", f.token, messagesBody(f.model.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	body := decodeJSON(t, w)
	if body["type"] != "message" || body["role"] != "assistant" {
		t.Errorf("envelope = %v", body)
	}
	if body["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v, want tool_use", body["stop_reason"])
	}

	blocks, _ := body["content"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("content = %v", body["content"])
	}
	block, _ := blocks[0].(map[string]any)
	if block["type"] != "tool_use" || block["name"] != "read_file" {
		t.Errorf("block = %v", block)
	}
	// An object here, not the JSON text the other protocol carries.
	input, ok := block["input"].(map[string]any)
	if !ok || input["path"] != "main.go" {
		t.Errorf("input = %v, want an object", block["input"])
	}
	if id, _ := block["id"].(string); id == "" {
		t.Error("the call has no id to answer it by")
	}
	if strings.Contains(w.Body.String(), "call_provider_side_id") {
		t.Errorf("the provider's own call id was forwarded: %s", w.Body.String())
	}
}

// The loop: the client replays the call as a tool_use block and answers it
// with a tool_result, which this protocol puts in a user turn. The provider
// is OpenAI-shaped, where a result is a message with a role of its own.
func TestMessagesCompletesTheToolLoop(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","max_tokens":1024,"messages":[` +
		`{"role":"user","content":"what is in main.go?"},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_x",` +
		`"name":"read_file","input":{"path":"main.go"}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_x",` +
		`"content":"package main"}]}]}`

	w := f.do(t, http.MethodPost, "/v1/messages", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	sent, _ := f.upstream.received()["messages"].([]any)
	if len(sent) != 3 {
		t.Fatalf("the provider was sent %v, want three messages", f.upstream.received()["messages"])
	}

	assistant, _ := sent[1].(map[string]any)
	calls, _ := assistant["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("the replayed call did not reach the provider: %v", assistant)
	}
	call, _ := calls[0].(map[string]any)
	made, _ := call["function"].(map[string]any)
	if call["id"] != "toolu_x" || made["name"] != "read_file" {
		t.Errorf("call = %v", call)
	}
	// An object became JSON text, which is what that protocol carries.
	if made["arguments"] != `{"path":"main.go"}` {
		t.Errorf("arguments = %v", made["arguments"])
	}

	// A result rides in a user turn here and is a message of its own there.
	result, _ := sent[2].(map[string]any)
	if result["role"] != "tool" || result["tool_call_id"] != "toolu_x" {
		t.Errorf("the result reached the provider as %v", result)
	}
	if result["content"] != "package main" {
		t.Errorf("result content = %v", result["content"])
	}
}

// A turn that answers a call and says something else in the same message: the
// result still has to become its own turn, or the provider sees a user
// message answering nothing.
func TestMessagesSplitsAToolResultFromTheTextBesideIt(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","max_tokens":1024,"messages":[` +
		`{"role":"user","content":"go"},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read_file","input":{}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"done"},` +
		`{"type":"text","text":"and now what?"}]}]}`

	w := f.do(t, http.MethodPost, "/v1/messages", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	sent, _ := f.upstream.received()["messages"].([]any)
	if len(sent) != 4 {
		t.Fatalf("messages = %v, want four: the result and the question are separate turns",
			f.upstream.received()["messages"])
	}
	result, _ := sent[2].(map[string]any)
	if result["role"] != "tool" {
		t.Errorf("third message = %v, want the tool result", result)
	}
	question, _ := sent[3].(map[string]any)
	if question["role"] != "user" || question["content"] != "and now what?" {
		t.Errorf("fourth message = %v, want the question", question)
	}
}

func TestMessagesReadsASystemPromptInBothShapes(t *testing.T) {
	f := newFixture(t)

	for _, system := range []string{
		`"one instruction"`,
		`[{"type":"text","text":"one instruction"}]`,
	} {
		f.upstream.reply(answer)
		body := `{"model":"` + f.model.ID + `","max_tokens":16,"system":` + system +
			`,"messages":[{"role":"user","content":"hi"}]}`
		w := f.do(t, http.MethodPost, "/v1/messages", f.token, body)
		if w.Code != http.StatusOK {
			t.Fatalf("system %s: status = %d: %s", system, w.Code, w.Body.String())
		}
		messages, _ := f.upstream.received()["messages"].([]any)
		first, _ := messages[0].(map[string]any)
		if first["content"] != "one instruction" {
			t.Errorf("system %s reached the provider as %v", system, first["content"])
		}
	}
}

func TestMessagesTranslatesToolChoice(t *testing.T) {
	f := newFixture(t)

	cases := map[string]any{
		`{"type":"auto"}`:                    nil,
		`{"type":"any"}`:                     "required",
		`{"type":"none"}`:                    "none",
		`{"type":"tool","name":"read_file"}`: "named",
	}
	for sent, want := range cases {
		f.upstream.reply(answer)
		body := strings.Replace(messagesBody(f.model.ID), `"stream":false`,
			`"stream":false,"tool_choice":`+sent, 1)
		w := f.do(t, http.MethodPost, "/v1/messages", f.token, body)
		if w.Code != http.StatusOK {
			t.Fatalf("tool_choice %s: status = %d: %s", sent, w.Code, w.Body.String())
		}

		got := f.upstream.received()["tool_choice"]
		switch want {
		case nil:
			if got != nil {
				t.Errorf("tool_choice %s went out as %v, want nothing", sent, got)
			}
		case "named":
			object, _ := got.(map[string]any)
			function, _ := object["function"].(map[string]any)
			if function["name"] != "read_file" {
				t.Errorf("tool_choice %s went out as %v", sent, got)
			}
		default:
			if got != want {
				t.Errorf("tool_choice %s went out as %v, want %v", sent, got, want)
			}
		}
	}
}

// A url source would have this server fetch an address of the caller's
// choosing, which is the request forgery primitive the other surface refuses
// for the same reason.
func TestMessagesRefusesARemoteImage(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","max_tokens":16,"messages":[{"role":"user",` +
		`"content":[{"type":"image","source":{"type":"url","url":"http://169.254.169.254/"}}]}]}`
	w := f.do(t, http.MethodPost, "/v1/messages", f.token, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// --- the streamed shape ---------------------------------------------------------

func TestMessagesStreamsTheEventProtocol(t *testing.T) {
	f := newFixture(t)
	f.upstream.stream(
		`{"choices":[{"delta":{"role":"assistant"}}]}`,
		`{"choices":[{"delta":{"content":"I will look."}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_upstream","type":"function",`+
			`"function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"main.go\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":40,"completion_tokens":9}}`,
	)

	body := strings.Replace(messagesBody(f.model.ID), `"stream":false`, `"stream":true`, 1)
	w := f.do(t, http.MethodPost, "/v1/messages", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	raw := w.Body.String()

	// The client tracks these by name, and a missing one leaves it waiting.
	for _, event := range []string{
		"event: message_start", "event: content_block_start",
		"event: content_block_delta", "event: content_block_stop",
		"event: message_delta", "event: message_stop",
	} {
		if !strings.Contains(raw, event) {
			t.Errorf("the stream never sent %q:\n%s", event, raw)
		}
	}

	// Text is block zero and the call is block one: a call cannot share a
	// block with the text before it.
	if !strings.Contains(raw, `"index":0`) || !strings.Contains(raw, `"index":1`) {
		t.Errorf("the blocks were not numbered:\n%s", raw)
	}
	if !strings.Contains(raw, `"type":"tool_use"`) {
		t.Errorf("no tool_use block was opened:\n%s", raw)
	}
	if !strings.Contains(raw, `{\"path\":\"main.go\"}`) {
		t.Errorf("the assembled arguments never went out:\n%s", raw)
	}
	if !strings.Contains(raw, `"stop_reason":"tool_use"`) {
		t.Errorf("the turn did not end in tool_use:\n%s", raw)
	}
	if strings.Contains(raw, "call_upstream") {
		t.Errorf("the provider's own call id was forwarded:\n%s", raw)
	}

	// The blocks have to close in order, or the client is left with one open.
	starts := strings.Count(raw, "event: content_block_start")
	stops := strings.Count(raw, "event: content_block_stop")
	if starts != stops || starts != 2 {
		t.Errorf("opened %d blocks and closed %d, want 2 and 2:\n%s", starts, stops, raw)
	}
}

// --- errors and counting --------------------------------------------------------

// This protocol's clients parse this envelope before they have anything else
// to go on, and the other surface's would read to them as no error at all.
func TestMessagesUsesItsOwnErrorEnvelope(t *testing.T) {
	f := newFixture(t)

	w := f.do(t, http.MethodPost, "/v1/messages", "sk-oa-not-a-real-key",
		messagesBody(f.model.ID))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}

	body := decodeJSON(t, w)
	if body["type"] != "error" {
		t.Errorf("envelope = %v, want type error", body)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope["type"] != "authentication_error" {
		t.Errorf("error type = %v, want authentication_error", envelope["type"])
	}
	if message, _ := envelope["message"].(string); message == "" {
		t.Error("the error carried no message")
	}
}

func TestMessagesRefusesAnUnknownContentBlock(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","max_tokens":16,"messages":[{"role":"user",` +
		`"content":[{"type":"video","text":"?"}]}]}`
	w := f.do(t, http.MethodPost, "/v1/messages", f.token, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if got := decodeJSON(t, w)["type"]; got != "error" {
		t.Errorf("envelope = %v", got)
	}
}

// Replayed reasoning is dropped rather than forwarded: it arrives with a
// signature this instance cannot have produced, and one that does not check
// out fails the whole turn upstream.
func TestMessagesDropsReplayedThinking(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","max_tokens":16,"messages":[` +
		`{"role":"user","content":"hi"},` +
		`{"role":"assistant","content":[{"type":"thinking","thinking":"secret reasoning",` +
		`"signature":"abc"},{"type":"text","text":"hello"}]},` +
		`{"role":"user","content":"again"}]}`

	w := f.do(t, http.MethodPost, "/v1/messages", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	encoded, err := json.Marshal(f.upstream.received())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret reasoning") {
		t.Errorf("a replayed thinking block was forwarded: %s", encoded)
	}
	if !strings.Contains(string(encoded), "hello") {
		t.Errorf("the text beside it was dropped too: %s", encoded)
	}
}

// An agent asks this to decide when to compact. A number that is close beats
// the 404 it used to get, which left it with none.
func TestCountTokensAnswersWithANumber(t *testing.T) {
	f := newFixture(t)

	w := f.do(t, http.MethodPost, "/v1/messages/count_tokens", f.token, messagesBody(f.model.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	count, ok := decodeJSON(t, w)["input_tokens"].(float64)
	if !ok || count <= 0 {
		t.Fatalf("input_tokens = %v", decodeJSON(t, w)["input_tokens"])
	}

	// The tool definitions are most of an agent's prompt, so a count that
	// left them out would be wrong in the direction that matters.
	bare := `{"model":"` + f.model.ID + `","max_tokens":16,` +
		`"messages":[{"role":"user","content":"what is in main.go?"}]}`
	w = f.do(t, http.MethodPost, "/v1/messages/count_tokens", f.token, bare)
	without, _ := decodeJSON(t, w)["input_tokens"].(float64)
	if without >= count {
		t.Errorf("counting with tools gave %v and without gave %v", count, without)
	}
}

// Counting is not answering: it must not reach a provider or spend anything.
func TestCountTokensDoesNotCallTheProvider(t *testing.T) {
	f := newFixture(t)
	f.upstream.fail(http.StatusInternalServerError, `{"error":{"message":"should not be called"}}`)

	w := f.do(t, http.MethodPost, "/v1/messages/count_tokens", f.token, messagesBody(f.model.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if turns := f.turns(); len(turns) != 0 {
		t.Errorf("counting recorded %d turns against the allowance", len(turns))
	}
}
