package adapter

import (
	"context"
	"strings"
	"testing"
)

// A model whose row says it cannot take the protocol's tool fields still has
// to serve an agent on the /v1 API. These tests pin the trade toolprompt.go
// makes on its behalf: what leaves for such a model, what its prose answers
// become on the way back, and which traffic is left entirely alone.

// toollessModel is a spec whose endpoint refuses tool fields — the row an
// operator marks when a model answers `tools` with a 400 or with prose.
func toollessModel() ModelSpec {
	spec := testModel()
	spec.SupportsTools = false
	return spec
}

// The routing decision itself: only a toolless model carrying tool traffic.
// An ordinary chat with the same model must not grow instructions, or every
// conversation would open with a lecture about a format it has no use for.
func TestNeedsToolEmulation(t *testing.T) {
	plain := []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}

	cases := []struct {
		name string
		req  ChatRequest
		want bool
	}{
		{"a tool-capable model with tools", ChatRequest{Model: testModel(), Tools: toolFixture()}, false},
		{"an ordinary chat", ChatRequest{Model: toollessModel(), Messages: plain}, false},
		{"tools offered", ChatRequest{Model: toollessModel(), Tools: toolFixture(), Messages: plain}, true},
		{"a replayed tool transcript", ChatRequest{Model: toollessModel(), Messages: toolTranscript()}, true},
	}
	for _, tc := range cases {
		if got := needsToolEmulation(tc.req); got != tc.want {
			t.Errorf("%s: needsToolEmulation = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// --- what leaves ----------------------------------------------------------------

// No tool fields anywhere, the catalogue in prose where the system prompt
// goes, and the transcript rewritten so the whole conversation reads in one
// voice: the model's own past call as the block it would have written, the
// result as a user turn answering it.
func TestEmulatedRequestTradesToolFieldsForInstructions(t *testing.T) {
	var sent map[string]any
	server := jsonServer(t, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`, &sent)

	var out collected
	if _, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: toollessModel(), System: "be brief",
			Messages: toolTranscript(), Tools: toolFixture(),
			ToolChoice: ToolChoice{Mode: ToolChoiceRequired},
		}, out.sink); err != nil {
		t.Fatalf("chat: %v", err)
	}

	if _, present := sent["tools"]; present {
		t.Error("a toolless model was sent the tools field")
	}
	if _, present := sent["tool_choice"]; present {
		t.Error("a toolless model was sent a tool_choice")
	}

	messages, _ := sent["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("messages = %v, want four", sent["messages"])
	}
	system, _ := messages[0].(map[string]any)
	if system["role"] != "system" {
		t.Fatalf("first message is %v, want the system prompt", system["role"])
	}
	instructions, _ := system["content"].(string)
	if !strings.Contains(instructions, "be brief") {
		t.Errorf("the caller's system prompt was dropped: %q", instructions)
	}
	if !strings.Contains(instructions, `"lookup"`) {
		t.Errorf("the instructions do not list the tool: %q", instructions)
	}
	if !strings.Contains(instructions, toolCallOpen) {
		t.Errorf("the instructions do not show the block format: %q", instructions)
	}
	if !strings.Contains(instructions, "must call one of the tools") {
		t.Errorf("a required choice was not said out loud: %q", instructions)
	}

	assistant, _ := messages[2].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Fatalf("third message is %v, want the assistant", assistant["role"])
	}
	if _, present := assistant["tool_calls"]; present {
		t.Error("the replayed call was sent as a tool_calls field")
	}
	call, _ := assistant["content"].(string)
	if !strings.Contains(call, `"lookup"`) || !strings.Contains(call, `"q":"go"`) {
		t.Errorf("the replayed call did not become a block: %q", call)
	}

	result, _ := messages[3].(map[string]any)
	if result["role"] != "user" {
		t.Errorf("the tool result was sent as role %v, want user", result["role"])
	}
	answer, _ := result["content"].(string)
	if !strings.Contains(answer, "a language") || !strings.Contains(answer, toolResponseOpen) {
		t.Errorf("the result did not become a response block: %q", answer)
	}
}

// A model with no system prompt would drop the instructions; they travel as
// the opening of the first user message instead, which is where a system-less
// model reads everything it must not forget.
func TestEmulatedInstructionsReachAModelWithoutASystemPrompt(t *testing.T) {
	spec := toollessModel()
	spec.SupportsSystem = false

	var sent map[string]any
	server := jsonServer(t, `{"choices":[{"message":{"content":"ok"}}]}`, &sent)

	var out collected
	if _, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model:    spec,
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "what is go"}}}},
			Tools:    toolFixture(),
		}, out.sink); err != nil {
		t.Fatalf("chat: %v", err)
	}

	messages, _ := sent["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %v, want one", sent["messages"])
	}
	first, _ := messages[0].(map[string]any)
	content, _ := first["content"].(string)
	if !strings.Contains(content, toolCallOpen) {
		t.Errorf("the instructions did not reach the first message: %q", content)
	}
	if !strings.Contains(content, "what is go") {
		t.Errorf("the caller's question was lost: %q", content)
	}
}

// `none` means the caller wants no calls made, so a toolless model is told
// nothing at all: teaching the format to a model that must not use it is how
// a forbidden call gets made anyway.
func TestChoiceNoneTeachesNothing(t *testing.T) {
	var sent map[string]any
	server := jsonServer(t, `{"choices":[{"message":{"content":"ok"}}]}`, &sent)

	var out collected
	if _, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: toollessModel(), Tools: toolFixture(),
			ToolChoice: ToolChoice{Mode: ToolChoiceNone},
			Messages:   []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
		}, out.sink); err != nil {
		t.Fatalf("chat: %v", err)
	}

	if _, present := sent["tools"]; present {
		t.Error("a forbidden tool list was still sent")
	}
	messages, _ := sent["messages"].([]any)
	for _, entry := range messages {
		message, _ := entry.(map[string]any)
		content, _ := message["content"].(string)
		if strings.Contains(content, toolCallOpen) {
			t.Errorf("the instructions travelled anyway: %q", content)
		}
	}
}

// --- what comes back ------------------------------------------------------------

// The block a toolless model writes is read back as the call it means: same
// events, same fields, as a native one — which is the whole point, since no
// layer above this one is allowed to know the difference.
func TestEmulatedAnswerBecomesToolCalls(t *testing.T) {
	server := jsonServer(t, `{"choices":[{"message":{"content":`+
		`"Let me check.\n<tool_call>\n{\"name\": \"lookup\", \"arguments\": {\"q\": \"go\"}}\n</tool_call>"},`+
		`"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":6}}`, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: toollessModel(), Tools: toolFixture(),
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "what is go"}}}},
		}, out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v, want one", result.ToolCalls)
	}
	got := result.ToolCalls[0]
	if got.Name != "lookup" || got.Arguments != `{"q": "go"}` {
		t.Errorf("call = %+v", got)
	}
	if got.ID == "" {
		t.Error("the call has no id to answer it by")
	}
	if !strings.HasPrefix(result.Text, "Let me check.") {
		t.Errorf("answer = %q, want the prose the model wrote around the block", result.Text)
	}
	if strings.Contains(result.Text, toolCallOpen) {
		t.Errorf("the block is still in the answer: %q", result.Text)
	}
	if result.FinishReason != "tool_calls" {
		t.Errorf("finish reason = %q, want tool_calls — a client branches on it", result.FinishReason)
	}
	if len(out.calls) != 1 || out.calls[0].Name != "lookup" {
		t.Errorf("events = %+v, want the call as an event too", out.calls)
	}
	if !strings.HasPrefix(out.answer.String(), "Let me check.") {
		t.Errorf("the sink saw %q", out.answer.String())
	}
	if result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 6 {
		t.Errorf("usage = %+v", result.Usage)
	}
}

// Several blocks in one reply are several calls, the way both protocols let a
// model open several at once and an agent runs them in parallel.
func TestEmulatedAnswerMayCarrySeveralCalls(t *testing.T) {
	server := jsonServer(t, `{"choices":[{"message":{"content":`+
		`"<tool_call>{\"name\":\"lookup\",\"arguments\":{\"q\":\"go\"}}</tool_call>` +
		`<tool_call>{\"name\":\"clock\",\"arguments\":{}}</tool_call>"},`+
		`"finish_reason":"stop"}]}`, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: toollessModel(), Tools: toolFixture(),
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "time?"}}}},
		}, out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if len(result.ToolCalls) != 2 {
		t.Fatalf("calls = %+v, want two", result.ToolCalls)
	}
	if result.ToolCalls[0].Name != "lookup" || result.ToolCalls[1].Name != "clock" {
		t.Errorf("calls = %+v", result.ToolCalls)
	}
	if result.ToolCalls[0].ID == result.ToolCalls[1].ID {
		t.Errorf("both calls share the id %q", result.ToolCalls[0].ID)
	}
	if result.Text != "" {
		t.Errorf("a turn that was nothing but calls became text %q", result.Text)
	}
}

// A stream splits the block anywhere, including inside the tags. The prose
// around it still arrives as it was written, and the tag fragments never
// surface as text that then has to be taken back.
func TestEmulatedStreamAssemblesCallsAcrossDeltas(t *testing.T) {
	frames := []string{
		`{"choices":[{"delta":{"content":"One "}}]}`,
		`{"choices":[{"delta":{"content":"moment. <tool_"}}]}`,
		`{"choices":[{"delta":{"content":"call>{\"name\": \"lookup\", \"argu"}}]}`,
		`{"choices":[{"delta":{"content":"ments\": {\"q\": \"go\"}}</tool_"}}]}`,
		`{"choices":[{"delta":{"content":"call>"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`,
	}
	server := sseServer(t, frames, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: toollessModel(), Stream: true, Tools: toolFixture(),
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "what is go"}}}},
		}, out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if out.answer.String() != "One moment. " {
		t.Errorf("streamed answer = %q", out.answer.String())
	}
	if !strings.HasPrefix(result.Text, "One moment.") {
		t.Errorf("final answer = %q", result.Text)
	}
	if strings.Contains(out.answer.String(), "<tool_") {
		t.Errorf("a tag fragment went out as text: %q", out.answer.String())
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("calls = %+v, want one", result.ToolCalls)
	}
	if got := result.ToolCalls[0]; got.Name != "lookup" || got.Arguments != `{"q": "go"}` {
		t.Errorf("call = %+v", got)
	}
	if result.FinishReason != "tool_calls" {
		t.Errorf("finish reason = %q", result.FinishReason)
	}
}

// A reply that mentions the format without making a call — a block that names
// no tool, or one holding nothing JSON at all — is prose, and stays visible:
// eating it would hide a reply behind a guess about what it meant.
func TestProseThatWearsTheTagsStaysVisible(t *testing.T) {
	server := jsonServer(t, `{"choices":[{"message":{"content":`+
		`"Write <tool_call>{\"not\":\"a call\"}</tool_call> to call a tool."},`+
		`"finish_reason":"stop"}]}`, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: toollessModel(), Tools: toolFixture(),
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "how?"}}}},
		}, out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if len(result.ToolCalls) != 0 {
		t.Errorf("prose became calls: %+v", result.ToolCalls)
	}
	if !strings.Contains(result.Text, toolCallOpen) {
		t.Errorf("the reply was hidden: %q", result.Text)
	}
	if result.FinishReason == "tool_calls" {
		t.Error("a reply with no calls was labelled tool_calls")
	}
}

// A turn with no blocks in it at all is an ordinary answer: same text, same
// finish reason, nothing invented.
func TestEmulatedAnswerWithoutCallsIsOrdinary(t *testing.T) {
	server := jsonServer(t, `{"choices":[{"message":{"content":"just an answer"},`+
		`"finish_reason":"stop"}]}`, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: toollessModel(), Tools: toolFixture(),
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
		}, out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if result.Text != "just an answer" {
		t.Errorf("answer = %q", result.Text)
	}
	if len(result.ToolCalls) != 0 {
		t.Errorf("calls = %+v", result.ToolCalls)
	}
	if result.FinishReason != "stop" {
		t.Errorf("finish reason = %q, want the provider's own", result.FinishReason)
	}
}

// The same emulation through the other protocol: the trade is made before any
// adapter sees the request, so an Anthropic endpoint that cannot take tools
// answers the same way an OpenAI one does.
func TestAnthropicEmulatedChat(t *testing.T) {
	server := jsonServer(t, `{"content":[{"type":"text","text":`+
		`"Checking.\n<tool_call>\n{\"name\": \"lookup\", \"arguments\": {\"q\": \"go\"}}\n</tool_call>"}],`+
		`"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindAnthropic, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: toollessModel(), Tools: toolFixture(),
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "what is go"}}}},
		}, out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if len(result.ToolCalls) != 1 {
		t.Fatalf("calls = %+v, want one", result.ToolCalls)
	}
	if got := result.ToolCalls[0]; got.Name != "lookup" || got.Arguments != `{"q": "go"}` {
		t.Errorf("call = %+v", got)
	}
	if !strings.HasPrefix(result.Text, "Checking.") {
		t.Errorf("answer = %q", result.Text)
	}
}

// --- reading the block ----------------------------------------------------------

// A model imitating a format writes it several ways. Every spelling that
// names a tool is a call; anything else is not, which is what separates a
// call from prose that mentions the format.
func TestParseToolCallBodyReadsTheWildSpellings(t *testing.T) {
	cases := []struct {
		name string
		body string
		// Empty name means the body must not parse as a call.
		wantName string
		wantArgs string
	}{
		{
			name: "the format as taught",
			body: `{"name": "lookup", "arguments": {"q": "go"}}`,
			wantName: "lookup", wantArgs: `{"q": "go"}`,
		},
		{
			name: "parameters for arguments",
			body: `{"name": "lookup", "parameters": {"q": "go"}}`,
			wantName: "lookup", wantArgs: `{"q": "go"}`,
		},
		{
			name: "nested the wire way",
			body: `{"function": {"name": "lookup", "arguments": {"q": "go"}}}`,
			wantName: "lookup", wantArgs: `{"q": "go"}`,
		},
		{
			name: "a code fence around the json",
			body: "```json\n{\"name\": \"lookup\", \"arguments\": {\"q\": \"go\"}}\n```",
			wantName: "lookup", wantArgs: `{"q": "go"}`,
		},
		{
			name: "arguments double-encoded",
			body: `{"name": "lookup", "arguments": "{\"q\": \"go\"}"}`,
			wantName: "lookup", wantArgs: `{"q": "go"}`,
		},
		{
			name: "no arguments at all",
			body: `{"name": "clock"}`,
			wantName: "clock", wantArgs: "{}",
		},
		{name: "no name", body: `{"arguments": {"q": "go"}}`},
		{name: "not json", body: `certainly, let me look`},
		{name: "empty", body: ``},
		{name: "a code fence with nothing in it", body: "```json\n```"},
	}

	for _, tc := range cases {
		call, ok := parseToolCallBody(tc.body)
		if tc.wantName == "" {
			if ok {
				t.Errorf("%s: parsed as a call: %+v", tc.name, call)
			}
			continue
		}
		if !ok {
			t.Errorf("%s: did not parse", tc.name)
			continue
		}
		if call.Name != tc.wantName {
			t.Errorf("%s: name = %q, want %q", tc.name, call.Name, tc.wantName)
		}
		if call.Arguments != tc.wantArgs {
			t.Errorf("%s: arguments = %q, want %q", tc.name, call.Arguments, tc.wantArgs)
		}
	}
}
