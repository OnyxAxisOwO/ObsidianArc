package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Tool calls are the one thing an agent client cannot do without, and the one
// place the two protocols disagree about almost every detail: where the
// schema lives, whether a result is a message or a block, whether the
// arguments are text or an object, and how a streamed call is framed. These
// tests pin each of those.

func toolFixture() []Tool {
	return []Tool{{
		Name:        "lookup",
		Description: "Look something up",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
	}}
}

// A transcript in the middle of an agent loop: the model asked, the client
// answered, and the whole exchange comes back on the next turn.
func toolTranscript() []Message {
	return []Message{
		{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "what is go"}}},
		{Role: RoleAssistant, Parts: []Part{{
			Kind: PartToolCall, ToolCallID: "call_1", ToolName: "lookup",
			ToolArgs: `{"q":"go"}`,
		}}},
		{Role: RoleTool, Parts: []Part{{
			Kind: PartToolResult, ToolCallID: "call_1", Text: "a language",
		}}},
	}
}

// jsonServer answers with one document, capturing what was asked of it.
func jsonServer(t *testing.T, body string, capture *map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			raw, _ := io.ReadAll(r.Body)
			decoded := map[string]any{}
			_ = json.Unmarshal(raw, &decoded)
			*capture = decoded
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

// --- what leaves for an OpenAI-compatible endpoint ------------------------------

func TestOpenAISendsToolsAndTheWholeToolTranscript(t *testing.T) {
	var sent map[string]any
	server := jsonServer(t, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`, &sent)

	var out collected
	if _, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: testModel(), Messages: toolTranscript(), Tools: toolFixture(),
			ToolChoice: ToolChoice{Mode: ToolChoiceRequired},
		}, out.sink); err != nil {
		t.Fatalf("chat: %v", err)
	}

	tools, _ := sent["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want one", sent["tools"])
	}
	entry, _ := tools[0].(map[string]any)
	if entry["type"] != "function" {
		t.Errorf("tool type = %v, want function", entry["type"])
	}
	function, _ := entry["function"].(map[string]any)
	if function["name"] != "lookup" {
		t.Errorf("tool name = %v", function["name"])
	}
	// The caller's schema, not a reading of it: an agent's tool stops
	// matching the function it implements the moment this is rewritten.
	schema, _ := function["parameters"].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	if _, ok := properties["q"]; !ok {
		t.Errorf("the parameter schema did not survive: %v", function["parameters"])
	}
	if sent["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v, want required", sent["tool_choice"])
	}

	messages, _ := sent["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %v, want three", sent["messages"])
	}

	assistant, _ := messages[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Fatalf("second message is %v", assistant["role"])
	}
	// Null, not missing and not empty: the protocol says a turn that was
	// nothing but calls has no content.
	if content, present := assistant["content"]; !present || content != nil {
		t.Errorf("assistant content = %v, want null", content)
	}
	calls, _ := assistant["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("tool_calls = %v", assistant["tool_calls"])
	}
	call, _ := calls[0].(map[string]any)
	if call["id"] != "call_1" {
		t.Errorf("call id = %v", call["id"])
	}
	made, _ := call["function"].(map[string]any)
	if made["name"] != "lookup" || made["arguments"] != `{"q":"go"}` {
		t.Errorf("call = %v", made)
	}

	result, _ := messages[2].(map[string]any)
	if result["role"] != "tool" {
		t.Errorf("third message role = %v, want tool", result["role"])
	}
	if result["tool_call_id"] != "call_1" {
		t.Errorf("result names call %v", result["tool_call_id"])
	}
	if result["content"] != "a language" {
		t.Errorf("result content = %v", result["content"])
	}
}

func TestOpenAINamedToolChoice(t *testing.T) {
	var sent map[string]any
	server := jsonServer(t, `{"choices":[{"message":{"content":"ok"}}]}`, &sent)

	var out collected
	if _, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: testModel(), Tools: toolFixture(),
			ToolChoice: ToolChoice{Mode: ToolChoiceNamed, Name: "lookup"},
			Messages:   []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
		}, out.sink); err != nil {
		t.Fatalf("chat: %v", err)
	}

	choice, _ := sent["tool_choice"].(map[string]any)
	function, _ := choice["function"].(map[string]any)
	if choice["type"] != "function" || function["name"] != "lookup" {
		t.Errorf("tool_choice = %v", sent["tool_choice"])
	}
}

// Nothing is sent for auto, which is what every endpoint does anyway, and a
// request with no tools carries no tool fields at all.
func TestNoToolsMeansNoToolFields(t *testing.T) {
	var sent map[string]any
	server := jsonServer(t, `{"choices":[{"message":{"content":"ok"}}]}`, &sent)

	var out collected
	if _, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model:    testModel(),
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
		}, out.sink); err != nil {
		t.Fatalf("chat: %v", err)
	}

	if _, present := sent["tools"]; present {
		t.Error("a request with no tools carried a tools field")
	}
	if _, present := sent["tool_choice"]; present {
		t.Error("a request with no tools carried a tool_choice")
	}
}

// --- what comes back from an OpenAI-compatible endpoint -------------------------

func TestOpenAIReadsBufferedToolCalls(t *testing.T) {
	server := jsonServer(t, `{"choices":[{"message":{"content":null,"tool_calls":[
		{"id":"call_x","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"go\"}"}}]},
		"finish_reason":"tool_calls"}]}`, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: testModel(), Tools: toolFixture(),
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
		}, out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool calls = %v, want one", result.ToolCalls)
	}
	got := result.ToolCalls[0]
	if got.ID != "call_x" || got.Name != "lookup" || got.Arguments != `{"q":"go"}` {
		t.Errorf("call = %+v", got)
	}
	if result.FinishReason != "tool_calls" {
		t.Errorf("finish reason = %q", result.FinishReason)
	}
}

// The arguments of a streamed call arrive a few characters at a time, and two
// calls in flight interleave. The index is the identity, not the order of
// arrival.
func TestOpenAIStreamAssemblesInterleavedToolCalls(t *testing.T) {
	frames := []string{
		`{"choices":[{"delta":{"content":"one moment"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"lookup","arguments":"{\"q\""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"clock","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"go\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`,
	}
	server := sseServer(t, frames, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: testModel(), Stream: true, Tools: toolFixture(),
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
		}, out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if out.answer.String() != "one moment" {
		t.Errorf("answer = %q", out.answer.String())
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v, want two", result.ToolCalls)
	}
	if got := result.ToolCalls[0]; got.ID != "call_a" || got.Name != "lookup" ||
		got.Arguments != `{"q":"go"}` {
		t.Errorf("first call = %+v", got)
	}
	// A call whose arguments were complete in one frame is not corrupted by
	// the one interleaved after it.
	if got := result.ToolCalls[1]; got.ID != "call_b" || got.Name != "clock" ||
		got.Arguments != "{}" {
		t.Errorf("second call = %+v", got)
	}
	// The sink saw the same calls, whole, so a caller that only watches
	// events is not left waiting for a result it never reads.
	if len(out.calls) != 2 || out.calls[0].Arguments != `{"q":"go"}` {
		t.Errorf("events = %+v", out.calls)
	}
}

// A server that repeats the whole name on every fragment is more common than
// one that splits it, so the name is assigned rather than appended.
func TestOpenAIStreamDoesNotRepeatToolNames(t *testing.T) {
	frames := []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"lookup","arguments":"{"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup","arguments":"}"}}]}}]}`,
	}
	server := sseServer(t, frames, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: testModel(), Stream: true,
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
		}, out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "lookup" {
		t.Errorf("calls = %+v", result.ToolCalls)
	}
}

// A server that answered a stream request with a document still has to hand
// its calls to the sink, or the fallback path silently drops them.
func TestToolCallsSurviveTheStreamFallback(t *testing.T) {
	server := jsonServer(t, `{"choices":[{"message":{"content":null,"tool_calls":[
		{"id":"call_x","type":"function","function":{"name":"lookup","arguments":"{}"}}]},
		"finish_reason":"tool_calls"}]}`, nil)

	var out collected
	if _, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: testModel(), Stream: true, Tools: toolFixture(),
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
		}, out.sink); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if len(out.calls) != 1 || out.calls[0].Name != "lookup" {
		t.Errorf("events = %+v", out.calls)
	}
}

// --- Anthropic ------------------------------------------------------------------

func TestAnthropicSendsToolsAndTheWholeToolTranscript(t *testing.T) {
	var sent map[string]any
	server := jsonServer(t, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{}}`, &sent)

	var out collected
	if _, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindAnthropic, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: testModel(), Messages: toolTranscript(), Tools: toolFixture(),
			ToolChoice: ToolChoice{Mode: ToolChoiceRequired},
		}, out.sink); err != nil {
		t.Fatalf("chat: %v", err)
	}

	tools, _ := sent["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want one", sent["tools"])
	}
	entry, _ := tools[0].(map[string]any)
	if entry["name"] != "lookup" {
		t.Errorf("tool name = %v", entry["name"])
	}
	// The same schema, under the name this protocol gives the field.
	if _, ok := entry["input_schema"].(map[string]any); !ok {
		t.Errorf("input_schema = %v", entry["input_schema"])
	}
	if _, ok := entry["parameters"]; ok {
		t.Error("the other protocol's field name was sent")
	}
	choice, _ := sent["tool_choice"].(map[string]any)
	if choice["type"] != "any" {
		t.Errorf(`tool_choice = %v, want {"type":"any"}`, sent["tool_choice"])
	}

	messages, _ := sent["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %v, want three", sent["messages"])
	}

	assistant, _ := messages[1].(map[string]any)
	blocks, _ := assistant["content"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("assistant content = %v", assistant["content"])
	}
	use, _ := blocks[0].(map[string]any)
	if use["type"] != "tool_use" || use["id"] != "call_1" || use["name"] != "lookup" {
		t.Errorf("tool_use = %v", use)
	}
	// An object here, where the other protocol carries JSON text.
	input, ok := use["input"].(map[string]any)
	if !ok || input["q"] != "go" {
		t.Errorf("input = %v, want an object", use["input"])
	}

	// A result is a user turn here, not a role of its own.
	answer, _ := messages[2].(map[string]any)
	if answer["role"] != "user" {
		t.Errorf("result role = %v, want user", answer["role"])
	}
	results, _ := answer["content"].([]any)
	if len(results) != 1 {
		t.Fatalf("result content = %v", answer["content"])
	}
	block, _ := results[0].(map[string]any)
	if block["type"] != "tool_result" || block["tool_use_id"] != "call_1" ||
		block["content"] != "a language" {
		t.Errorf("tool_result = %v", block)
	}
}

// The API refuses a turn whose results do not head it, and an agent that also
// says something alongside its result is ordinary.
func TestAnthropicToolResultsHeadTheirTurn(t *testing.T) {
	var sent map[string]any
	server := jsonServer(t, `{"content":[{"type":"text","text":"ok"}],"usage":{}}`, &sent)

	var out collected
	if _, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindAnthropic, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: testModel(), Tools: toolFixture(),
			Messages: []Message{
				{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "what is go"}}},
				{Role: RoleAssistant, Parts: []Part{{
					Kind: PartToolCall, ToolCallID: "call_1", ToolName: "lookup", ToolArgs: `{}`,
				}}},
				{Role: RoleTool, Parts: []Part{{
					Kind: PartToolResult, ToolCallID: "call_1", Text: "a language",
				}}},
				{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "and now?"}}},
			},
		}, out.sink); err != nil {
		t.Fatalf("chat: %v", err)
	}

	messages, _ := sent["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %v, want three: the result and the question merge", sent["messages"])
	}
	last, _ := messages[2].(map[string]any)
	blocks, _ := last["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("merged turn = %v", last["content"])
	}
	first, _ := blocks[0].(map[string]any)
	if first["type"] != "tool_result" {
		t.Errorf("turn opens with %v, want tool_result", first["type"])
	}
	second, _ := blocks[1].(map[string]any)
	if second["type"] != "text" {
		t.Errorf("turn continues with %v, want text", second["type"])
	}
}

// Forcing a tool is refused while thinking is on, so the push is dropped
// rather than made into a failed turn — the same trade the temperature makes.
func TestAnthropicDropsAForcedToolChoiceWhileThinking(t *testing.T) {
	var sent map[string]any
	server := jsonServer(t, `{"content":[{"type":"text","text":"ok"}],"usage":{}}`, &sent)

	call := func(mode ToolChoiceMode) {
		t.Helper()
		var out collected
		if _, err := testRegistry().Chat(context.Background(),
			Provider{Kind: KindAnthropic, BaseURL: server.URL, APIKey: "k"},
			ChatRequest{
				Model: testModel(), Tools: toolFixture(),
				Reasoning:  Reasoning{Enabled: true, Effort: EffortLow},
				ToolChoice: ToolChoice{Mode: mode, Name: "lookup"},
				Messages:   []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
			}, out.sink); err != nil {
			t.Fatalf("chat: %v", err)
		}
	}

	call(ToolChoiceRequired)
	if _, present := sent["tool_choice"]; present {
		t.Errorf("a forced choice was sent alongside thinking: %v", sent["tool_choice"])
	}
	if _, present := sent["tools"]; !present {
		t.Error("the tools themselves were dropped, which was not the point")
	}

	// "none" is allowed there, and means something the caller asked for.
	call(ToolChoiceNone)
	choice, _ := sent["tool_choice"].(map[string]any)
	if choice["type"] != "none" {
		t.Errorf("tool_choice = %v, want none", sent["tool_choice"])
	}
}

func TestAnthropicReadsBufferedToolUse(t *testing.T) {
	server := jsonServer(t, `{"content":[
		{"type":"text","text":"looking"},
		{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"q":"go"}}],
		"stop_reason":"tool_use","usage":{"input_tokens":8,"output_tokens":3}}`, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindAnthropic, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: testModel(), Tools: toolFixture(),
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
		}, out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if result.Text != "looking" {
		t.Errorf("text = %q", result.Text)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", result.ToolCalls)
	}
	got := result.ToolCalls[0]
	if got.ID != "toolu_1" || got.Name != "lookup" || got.Arguments != `{"q":"go"}` {
		t.Errorf("call = %+v", got)
	}
	if result.FinishReason != "tool_use" {
		t.Errorf("finish reason = %q", result.FinishReason)
	}
}

// A call is its own content block: a start naming it, the arguments as JSON
// fragments, then a stop. It is emitted at the stop, which is the first
// moment the arguments are whole.
func TestAnthropicStreamAssemblesToolCalls(t *testing.T) {
	frames := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":8}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"looking"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"go\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}`,
	}
	server := sseServer(t, frames, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindAnthropic, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model: testModel(), Stream: true, Tools: toolFixture(),
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
		}, out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if out.answer.String() != "looking" {
		t.Errorf("answer = %q", out.answer.String())
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", result.ToolCalls)
	}
	if got := result.ToolCalls[0]; got.ID != "toolu_1" || got.Name != "lookup" ||
		got.Arguments != `{"q":"go"}` {
		t.Errorf("call = %+v", got)
	}
	if len(out.calls) != 1 {
		t.Errorf("events = %+v, want one", out.calls)
	}
	if result.FinishReason != "tool_use" {
		t.Errorf("finish reason = %q", result.FinishReason)
	}
}

// --- the shapes that must never leave malformed ---------------------------------

// An empty arguments field is not valid JSON, and a client that decodes it
// fails on a call the model made correctly.
func TestArgumentsAreAlwaysParseableJSON(t *testing.T) {
	if got := toolArguments(""); got != "{}" {
		t.Errorf("toolArguments(empty) = %q", got)
	}
	if got := toolArguments("  "); got != "{}" {
		t.Errorf("toolArguments(blank) = %q", got)
	}
	if got := toolArguments(`{"q":1}`); got != `{"q":1}` {
		t.Errorf("toolArguments passed through as %q", got)
	}
}

// Arguments the model wrote badly cost one call. Sending them raw would put
// malformed JSON in the request body, which costs the whole turn.
func TestUnparseableArgumentsBecomeAnEmptyObject(t *testing.T) {
	if got := string(toolInput(`{"q":`)); got != "{}" {
		t.Errorf("toolInput(truncated) = %q", got)
	}
	if got := string(toolInput(`{"q":"go"}`)); got != `{"q":"go"}` {
		t.Errorf("toolInput passed through as %q", got)
	}
}

// A tool that takes no arguments still needs a schema: several servers refuse
// a function whose parameters are missing.
func TestAToolWithNoSchemaStillCarriesOne(t *testing.T) {
	encoded, err := json.Marshal(schemaOrEmpty(nil))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["type"] != "object" {
		t.Errorf("empty schema = %s", encoded)
	}
}
