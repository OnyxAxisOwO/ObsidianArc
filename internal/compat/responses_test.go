package compat

import (
	"net/http"
	"strings"
	"testing"
)

// The Responses surface. Its own vocabulary throughout: items rather than
// messages, a flat tool rather than a nested one, and a stream whose unit is
// the output item.

func responsesBody(modelName string) string {
	return `{"model":"` + modelName + `","stream":false,"store":false,` +
		`"instructions":"You are helpful.",` +
		`"tools":[{"type":"function","name":"read_file","description":"Read a file",` +
		`"parameters":{"type":"object","properties":{"path":{"type":"string"}}}}],` +
		`"input":[{"type":"message","role":"user",` +
		`"content":[{"type":"input_text","text":"what is in main.go?"}]}]}`
}

func TestResponsesTranslatesAFlatToolOnTheWayOut(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(toolAnswer)

	w := f.do(t, http.MethodPost, "/v1/responses", f.token, responsesBody(f.model.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	// Flat on the way in, nested under `function` on the way out.
	tools, _ := f.upstream.received()["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("the provider was sent %v", f.upstream.received()["tools"])
	}
	entry, _ := tools[0].(map[string]any)
	function, _ := entry["function"].(map[string]any)
	if function["name"] != "read_file" {
		t.Errorf("tool name = %v", function["name"])
	}

	// `instructions` is this protocol's system prompt.
	messages, _ := f.upstream.received()["messages"].([]any)
	first, _ := messages[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "You are helpful." {
		t.Errorf("instructions reached the provider as %v", first)
	}
}

// A namespace is a group, not a tool. Codex sends one for its sub-agent
// tools, and the functions inside are what the model can actually call.
func TestResponsesFlattensAToolNamespace(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","store":false,` +
		`"tools":[{"type":"namespace","name":"agents","description":"Sub-agents",` +
		`"tools":[{"type":"function","name":"spawn_agent","parameters":{"type":"object"}},` +
		`{"type":"function","name":"close_agent","parameters":{"type":"object"}}]}],` +
		`"input":"hi"}`

	w := f.do(t, http.MethodPost, "/v1/responses", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	tools, _ := f.upstream.received()["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("the namespace was not flattened: %v", f.upstream.received()["tools"])
	}
	names := map[string]bool{}
	for _, entry := range tools {
		tool, _ := entry.(map[string]any)
		function, _ := tool["function"].(map[string]any)
		name, _ := function["name"].(string)
		names[name] = true
	}
	if !names["spawn_agent"] || !names["close_agent"] {
		t.Errorf("the inner tools did not survive: %v", names)
	}
	// The group itself is not something the model can call.
	if names["agents"] {
		t.Error("the namespace was offered as a tool of its own")
	}
}

// Codex sends web_search on every request. Refusing it would stop the agent
// starting at all; a model never told about it cannot call it.
func TestResponsesDropsAHostedToolRatherThanRefusing(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","store":false,` +
		`"tools":[{"type":"web_search"},` +
		`{"type":"function","name":"read_file","parameters":{"type":"object"}}],` +
		`"input":"hi"}`

	w := f.do(t, http.MethodPost, "/v1/responses", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	tools, _ := f.upstream.received()["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want only the function", f.upstream.received()["tools"])
	}
	if strings.Contains(w.Body.String(), "web_search") {
		t.Error("the hosted tool leaked into the answer")
	}
}

func TestResponsesAnswersWithOutputItems(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(toolAnswer)

	w := f.do(t, http.MethodPost, "/v1/responses", f.token, responsesBody(f.model.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	body := decodeJSON(t, w)
	if body["object"] != "response" || body["status"] != "completed" {
		t.Errorf("envelope = %v", body)
	}

	output, _ := body["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("output = %v", body["output"])
	}
	item, _ := output[0].(map[string]any)
	if item["type"] != "function_call" || item["name"] != "read_file" {
		t.Errorf("item = %v", item)
	}
	// JSON text here, as on the chat surface.
	if item["arguments"] != `{"path":"main.go"}` {
		t.Errorf("arguments = %v", item["arguments"])
	}
	// The item's own id and the id the client answers are different fields,
	// and a client that confuses them addresses its result at nothing.
	if item["id"] == item["call_id"] {
		t.Errorf("id and call_id are the same value: %v", item)
	}
	if callID, _ := item["call_id"].(string); callID == "" {
		t.Error("the call has no call_id to answer it by")
	}
	if strings.Contains(w.Body.String(), "call_provider_side_id") {
		t.Errorf("the provider's own call id was forwarded: %s", w.Body.String())
	}

	// Renamed, not passed through: this protocol counts in input_tokens
	// where the provider said prompt_tokens.
	usage, _ := body["usage"].(map[string]any)
	if usage["input_tokens"] != float64(40) || usage["total_tokens"] != float64(49) {
		t.Errorf("usage = %v", usage)
	}
}

func TestResponsesCompletesTheToolLoop(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","store":false,"input":[` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"what is in main.go?"}]},` +
		`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file",` +
		`"arguments":"{\"path\":\"main.go\"}","status":"completed"},` +
		`{"type":"function_call_output","call_id":"call_1","output":"package main"}]}`

	w := f.do(t, http.MethodPost, "/v1/responses", f.token, body)
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
	// call_id, not the item id: it is the one the result names.
	if call["id"] != "call_1" {
		t.Errorf("the call reached the provider as %v", call["id"])
	}

	result, _ := sent[2].(map[string]any)
	if result["role"] != "tool" || result["tool_call_id"] != "call_1" {
		t.Errorf("the result reached the provider as %v", result)
	}
	if result["content"] != "package main" {
		t.Errorf("result content = %v", result["content"])
	}
}

func TestResponsesReadsABareStringInput(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","store":false,"input":"just this"}`
	w := f.do(t, http.MethodPost, "/v1/responses", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	messages, _ := f.upstream.received()["messages"].([]any)
	last, _ := messages[len(messages)-1].(map[string]any)
	if last["role"] != "user" || last["content"] != "just this" {
		t.Errorf("the prompt reached the provider as %v", last)
	}
}

// Codex replays these. What they carry is an encrypted payload only its
// author can read, so it is dropped rather than forwarded.
func TestResponsesDropsReplayedReasoningItems(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","store":false,"input":[` +
		`{"type":"message","role":"user","content":"hi"},` +
		`{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"secret-payload"},` +
		`{"type":"message","role":"user","content":"again"}]}`

	w := f.do(t, http.MethodPost, "/v1/responses", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(strings.Join(upstreamStrings(t, f), " "), "secret-payload") {
		t.Error("a replayed reasoning item was forwarded")
	}
}

// Both mean the server is keeping the conversation, and this one keeps
// nothing. Answering as though it had would silently drop every earlier turn.
func TestResponsesRefusesStatefulRequests(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	for _, body := range []string{
		`{"model":"` + f.model.ID + `","store":true,"input":"hi"}`,
		`{"model":"` + f.model.ID + `","store":false,"previous_response_id":"resp_x","input":"hi"}`,
	} {
		w := f.do(t, http.MethodPost, "/v1/responses", f.token, body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 for %s: %s", w.Code, body, w.Body.String())
		}
	}
}

// --- the streamed shape ---------------------------------------------------------

func TestResponsesStreamsItsEventProtocol(t *testing.T) {
	f := newFixture(t)
	f.upstream.stream(
		`{"choices":[{"delta":{"role":"assistant"}}]}`,
		`{"choices":[{"delta":{"content":"I will look."}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_upstream","type":"function",`+
			`"function":{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":40,"completion_tokens":9}}`,
	)

	body := strings.Replace(responsesBody(f.model.ID), `"stream":false`, `"stream":true`, 1)
	w := f.do(t, http.MethodPost, "/v1/responses", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	raw := w.Body.String()

	// A client builds its turn from the item that closes and from the
	// completion, so those are the events that must be right even when the
	// deltas between them are not read.
	for _, event := range []string{
		"event: response.created",
		"event: response.output_item.added",
		"event: response.output_text.delta",
		"event: response.output_item.done",
		"event: response.function_call_arguments.done",
		"event: response.completed",
	} {
		if !strings.Contains(raw, event) {
			t.Errorf("the stream never sent %q:\n%s", event, raw)
		}
	}
	if !strings.Contains(raw, `"sequence_number"`) {
		t.Errorf("events carried no sequence number:\n%s", raw)
	}
	if !strings.Contains(raw, `{\"path\":\"main.go\"}`) {
		t.Errorf("the assembled arguments never went out:\n%s", raw)
	}
	if !strings.Contains(raw, `"status":"completed"`) {
		t.Errorf("the response never completed:\n%s", raw)
	}
	if strings.Contains(raw, "call_upstream") {
		t.Errorf("the provider's own call id was forwarded:\n%s", raw)
	}

	// Text is one item and the call is the next, so the client can tell them
	// apart by position.
	if strings.Count(raw, "event: response.output_item.done") != 2 {
		t.Errorf("expected two finished items:\n%s", raw)
	}
}

// upstreamStrings renders what the provider was sent, for tests that only
// need to know whether something appears in it at all.
func upstreamStrings(t *testing.T, f *fixture) []string {
	t.Helper()
	out := []string{}
	messages, _ := f.upstream.received()["messages"].([]any)
	for _, message := range messages {
		entry, _ := message.(map[string]any)
		for _, value := range entry {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
	}
	return out
}

// --- thinking -----------------------------------------------------------------

// The thinking a reasoning model produces. It reached the chat/completions
// surface as `reasoning_content` from the day tools did, and this one dropped
// it on the floor: a Codex user on a reasoning model watched nothing happen
// until the answer arrived whole.
const thinkingReply = `{"choices":[{"message":{"content":"42.",` +
	`"reasoning_content":"Count the ways."},"finish_reason":"stop"}],` +
	`"usage":{"prompt_tokens":11,"completion_tokens":7}}`

func TestResponsesCarriesTheModelsThinking(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(thinkingReply)

	w := f.do(t, http.MethodPost, "/v1/responses", f.token,
		`{"model":"`+f.model.ID+`","store":false,"input":"what is the answer?"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	output, _ := decodeJSON(t, w)["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("expected the thinking and the answer, got %v", output)
	}

	// The thinking comes first, because that is the order it happened in.
	first, _ := output[0].(map[string]any)
	if first["type"] != "reasoning" {
		t.Fatalf("the first item is %v", first)
	}
	summary, _ := first["summary"].([]any)
	if len(summary) != 1 {
		t.Fatalf("summary = %v", first["summary"])
	}
	part, _ := summary[0].(map[string]any)
	if part["type"] != "summary_text" || part["text"] != "Count the ways." {
		t.Errorf("summary part = %v", part)
	}

	second, _ := output[1].(map[string]any)
	if second["type"] != "message" {
		t.Errorf("the answer did not follow the thinking: %v", second)
	}
}

func TestResponsesStreamsThinkingAsItsOwnItem(t *testing.T) {
	f := newFixture(t)
	f.upstream.stream(
		`{"choices":[{"delta":{"role":"assistant"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"Count "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"the ways."}}]}`,
		`{"choices":[{"delta":{"content":"42."}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`,
	)

	w := f.do(t, http.MethodPost, "/v1/responses", f.token,
		`{"model":"`+f.model.ID+`","stream":true,"store":false,"input":"what is the answer?"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	raw := w.Body.String()

	// The three events a client builds a thinking block from.
	for _, event := range []string{
		"event: response.reasoning_summary_part.added",
		"event: response.reasoning_summary_text.delta",
		"event: response.reasoning_summary_text.done",
	} {
		if !strings.Contains(raw, event) {
			t.Errorf("the stream never sent %q:\n%s", event, raw)
		}
	}

	// Assembled from its fragments, like the answer beside it.
	if !strings.Contains(raw, `"text":"Count the ways."`) {
		t.Errorf("the thinking never arrived whole:\n%s", raw)
	}

	// It closes before the answer opens, so a client rendering items in the
	// order it is sent them shows the thinking above the answer rather than
	// interleaved with it.
	closed := strings.Index(raw, "event: response.reasoning_summary_text.done")
	answered := strings.Index(raw, "event: response.output_text.delta")
	if closed < 0 || answered < 0 || closed > answered {
		t.Errorf("the thinking did not close before the answer began:\n%s", raw)
	}

	// Three finished items would mean the thinking was left open and closed
	// again beside the message.
	if got := strings.Count(raw, "event: response.output_item.done"); got != 2 {
		t.Errorf("finished items = %d, want the thinking and the message:\n%s", got, raw)
	}
	if !strings.Contains(raw, `"type":"reasoning"`) {
		t.Errorf("no reasoning item in the stream:\n%s", raw)
	}
}

// --- a turn that was cut short --------------------------------------------------

const truncatedReply = `{"choices":[{"message":{"content":"The first thing to say is"},` +
	`"finish_reason":"length"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`

func TestResponsesSaysWhenATurnWasCutShort(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(truncatedReply)

	w := f.do(t, http.MethodPost, "/v1/responses", f.token,
		`{"model":"`+f.model.ID+`","store":false,"input":"tell me everything"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	body := decodeJSON(t, w)
	if body["status"] != "incomplete" {
		t.Errorf("status = %v, want incomplete", body["status"])
	}
	details, _ := body["incomplete_details"].(map[string]any)
	if details["reason"] != "max_output_tokens" {
		t.Errorf("incomplete_details = %v", body["incomplete_details"])
	}
}

func TestResponsesStreamEndsWithIncompleteWhenCutShort(t *testing.T) {
	f := newFixture(t)
	f.upstream.stream(
		`{"choices":[{"delta":{"content":"The first thing to say is"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`,
	)

	w := f.do(t, http.MethodPost, "/v1/responses", f.token,
		`{"model":"`+f.model.ID+`","stream":true,"store":false,"input":"tell me everything"}`)
	raw := w.Body.String()

	if !strings.Contains(raw, "event: response.incomplete") {
		t.Errorf("the stream never said the turn was cut short:\n%s", raw)
	}
	// A client that reads only the closing event must not be told the answer
	// is whole, so the event that says so is not sent as well.
	if strings.Contains(raw, "event: response.completed") {
		t.Errorf("a truncated turn also reported completion:\n%s", raw)
	}
	if !strings.Contains(raw, `"reason":"max_output_tokens"`) {
		t.Errorf("the reason never reached the client:\n%s", raw)
	}
}
