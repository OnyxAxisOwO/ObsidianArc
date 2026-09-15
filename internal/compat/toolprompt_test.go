package compat

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

// A model an administrator marked as not taking tool fields still has to
// carry an agent's loop on the /v1 API: the gateway teaches it the format in
// prose and reads its answers back as calls. These tests are that loop, end
// to end, the way a client that knows nothing about any of it would drive it.

// toollessFixture is the ordinary fixture with the model's tool support
// turned off, which is the row an operator sets when an endpoint refuses the
// tools field.
func toollessFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	off := false
	if _, err := f.models.Update(context.Background(), f.model.ID,
		model.Update{SupportsTools: &off}); err != nil {
		t.Fatal(err)
	}
	return f
}

// The answer of a model that writes its call in prose: same shape as a native
// one — minted id, text arguments, finish_reason an agent can branch on —
// while the upstream side of the same request carried no tool fields at all.
func TestAToollessModelAnswersToolsThroughProse(t *testing.T) {
	f := toollessFixture(t)
	f.upstream.reply(`{"choices":[{"message":{"content":` +
		`"Let me look.\n<tool_call>\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"main.go\"}}\n</tool_call>"},` +
		`"finish_reason":"stop"}],"usage":{"prompt_tokens":40,"completion_tokens":9}}`)

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, toolsBody(f.model.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	sent := f.upstream.received()
	if _, present := sent["tools"]; present {
		t.Error("a toolless model was sent the tools field")
	}
	messages, _ := sent["messages"].([]any)
	if len(messages) == 0 {
		t.Fatalf("the provider was sent no messages: %v", sent["messages"])
	}
	system, _ := messages[0].(map[string]any)
	instructions, _ := system["content"].(string)
	if !strings.Contains(instructions, "read_file") {
		t.Errorf("the instructions never named the tool: %q", instructions)
	}
	if !strings.Contains(instructions, "<tool_call>") {
		t.Errorf("the instructions never showed the format: %q", instructions)
	}

	body := decodeJSON(t, w)
	choices, _ := body["choices"].([]any)
	first, _ := choices[0].(map[string]any)
	if first["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", first["finish_reason"])
	}
	message, _ := first["message"].(map[string]any)
	if content, _ := message["content"].(string); !strings.HasPrefix(content, "Let me look.") {
		t.Errorf("content = %q, want the prose around the block", content)
	}
	calls, _ := message["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("tool_calls = %v", message["tool_calls"])
	}
	call, _ := calls[0].(map[string]any)
	if id, _ := call["id"].(string); id == "" {
		t.Error("the call has no id to answer it by")
	}
	made, _ := call["function"].(map[string]any)
	if made["name"] != "read_file" {
		t.Errorf("call name = %v", made["name"])
	}
	// Byte for byte what the model wrote, as a native caller would have had it.
	if made["arguments"] != `{"path": "main.go"}` {
		t.Errorf("arguments = %v", made["arguments"])
	}
}

// The loop's second turn: the client replays the call it was handed and
// answers it, and the toolless upstream sees one conversation in prose — its
// own call as the block it wrote, the result as a user message — with no
// protocol fields it would refuse.
func TestTheEmulatedLoopCompletes(t *testing.T) {
	f := toollessFixture(t)
	f.upstream.reply(`{"choices":[{"message":{"content":` +
		`"<tool_call>\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"main.go\"}}\n</tool_call>"},` +
		`"finish_reason":"stop"}]}`)

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

	f.upstream.reply(answer)
	replay := `{"model":"` + f.model.ID + `","stream":false,` +
		`"tools":[{"type":"function","function":{"name":"read_file",` +
		`"description":"Read a file","parameters":{"type":"object",` +
		`"properties":{"path":{"type":"string"}},"required":["path"]}}}],` +
		`"messages":[{"role":"user","content":"what is in main.go"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"` + id + `",` +
		`"type":"function","function":{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}}]},` +
		`{"role":"tool","tool_call_id":"` + id + `","content":"package main"}]}`

	w = f.do(t, http.MethodPost, "/v1/chat/completions", f.token, replay)
	if w.Code != http.StatusOK {
		t.Fatalf("second turn: status = %d: %s", w.Code, w.Body.String())
	}

	sent := f.upstream.received()
	if _, present := sent["tools"]; present {
		t.Error("the replayed turn still carried the tools field")
	}
	messages, _ := sent["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("the provider was sent %d messages, want four: %v", len(messages), messages)
	}
	assistant, _ := messages[2].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Fatalf("third message role = %v, want assistant", assistant["role"])
	}
	if _, present := assistant["tool_calls"]; present {
		t.Error("the replayed call was sent as a tool_calls field")
	}
	replayed, _ := assistant["content"].(string)
	if !strings.Contains(replayed, `"read_file"`) ||
		!strings.Contains(replayed, `{"path":"main.go"}`) {
		t.Errorf("the replayed call did not become a block: %q", replayed)
	}
	result, _ := messages[3].(map[string]any)
	if result["role"] != "user" {
		t.Errorf("the tool result was sent as role %v, want user", result["role"])
	}
	answer, _ := result["content"].(string)
	if !strings.Contains(answer, "<tool_response>") || !strings.Contains(answer, "package main") {
		t.Errorf("the result did not become a response block: %q", answer)
	}
}

// A streaming client is the ordinary kind, and the block arrives in pieces
// there too: the prose as deltas, the call as one whole chunk keyed by its
// position, and the tag itself never on the wire.
func TestEmulatedCallsReachAStreamingClient(t *testing.T) {
	f := toollessFixture(t)
	f.upstream.stream(
		`{"choices":[{"delta":{"content":"Working "}}]}`,
		`{"choices":[{"delta":{"content":"<tool_call>{\"name\": \"read_file\", \"arg"}}]}`,
		`{"choices":[{"delta":{"content":"uments\": {\"path\": \"main.go\"}}</tool_call>"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":40,"completion_tokens":9}}`,
	)

	body := strings.Replace(toolsBody(f.model.ID), `"stream":false`, `"stream":true`, 1)
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	raw := w.Body.String()
	if !strings.Contains(raw, `"content":"Working "`) {
		t.Errorf("the prose never went out as a delta: %s", raw)
	}
	if !strings.Contains(raw, `"name":"read_file"`) {
		t.Errorf("the call never went out: %s", raw)
	}
	if !strings.Contains(raw, `"index":0`) {
		t.Errorf("the streamed call carried no index: %s", raw)
	}
	if !strings.Contains(raw, `"finish_reason":"tool_calls"`) {
		t.Errorf("the stream did not finish with tool_calls: %s", raw)
	}
	if strings.Contains(raw, "<tool_call>") {
		t.Errorf("the format's own tag was sent to the client: %s", raw)
	}
	if !strings.Contains(raw, "[DONE]") {
		t.Errorf("the stream did not end: %s", raw)
	}
}
