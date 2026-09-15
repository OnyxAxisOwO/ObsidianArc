package adapter

// Tool calling for a model that cannot carry it on the wire.
//
// `supports_tools` is off for a model whose endpoint refuses the tools field
// or that has never learned to answer with one. An agent on the /v1 API
// still needs its calls, so the registry teaches the format in prose
// instead: the tool list and a fixed tag format go into the system prompt,
// the transcript's tool turns are rewritten as ordinary text, and the answer
// is read back for tool_call blocks that become real ToolCalls — the same
// events a native caller would have seen, so no layer above this one can
// tell the difference.
//
// The tag pair is Qwen's, deliberately: more models have seen
// <tool_call>{"name": ..., "arguments": ...}</tool_call> in training data
// than any other spelling, and a format the model already knows is one it
// will follow.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const (
	toolCallOpen      = "<tool_call>"
	toolCallClose     = "</tool_call>"
	toolResponseOpen  = "<tool_response>"
	toolResponseClose = "</tool_response>"
)

// needsToolEmulation reports whether tool traffic has to move into prose for
// this request: the model's row says the protocol's tool fields are not for
// it, and the request is either offering tools or replaying a transcript
// that already contains tool turns. An ordinary chat with such a model — no
// tools, no history — passes through untouched.
func needsToolEmulation(req ChatRequest) bool {
	if req.Model.SupportsTools {
		return false
	}
	if len(req.Tools) > 0 {
		return true
	}
	for _, message := range req.Messages {
		for _, part := range message.Parts {
			if part.Kind == PartToolCall || part.Kind == PartToolResult {
				return true
			}
		}
	}
	return false
}

// emulatedChat runs one request through the prose protocol: the rewrite
// below shapes what leaves, the filter shapes what comes back, and the
// caller's sink sees exactly the events a native tool call would have
// produced.
func emulatedChat(
	ctx context.Context, a Adapter, client *http.Client, p Provider,
	req ChatRequest, sink Sink,
) (Result, error) {
	filter := &toolCallFilter{sink: sink}
	result, err := a.Chat(ctx, client, p, rewriteForEmulation(req), filter.event)
	if err != nil {
		return result, err
	}
	if err := filter.finish(&result); err != nil {
		return result, err
	}
	return result, nil
}

// --- the request that leaves ---------------------------------------------------

// rewriteForEmulation is the request an emulated model is actually sent: no
// tool fields anywhere, the transcript in prose, and — when tools are
// offered and not forbidden — the instructions for writing calls in their
// place.
func rewriteForEmulation(req ChatRequest) ChatRequest {
	out := req
	out.Tools = nil
	out.ToolChoice = ToolChoice{}
	out.Messages = rewriteToolTranscript(req.Messages)

	if len(req.Tools) > 0 && req.ToolChoice.Mode != ToolChoiceNone {
		block := toolInstructions(req.Tools, req.ToolChoice)
		if req.Model.SupportsSystem {
			if out.System != "" {
				out.System += "\n\n"
			}
			out.System += block
			return out
		}
		// Both adapters drop a system prompt a model cannot take, so the
		// instructions travel as the opening of the first user message
		// instead — dropped would mean a model that never learns the format.
		out.Messages = prependInstructions(out.Messages, block)
	}
	return out
}

// rewriteToolTranscript turns the tool turns of a transcript into the prose
// an endpoint without tool fields can carry: the calls an assistant made
// become the blocks it would have written, and the results become a user
// message answering them. Consecutive results share one message, the way
// the calls that prompted them shared one reply.
func rewriteToolTranscript(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	var results strings.Builder

	flush := func() {
		if results.Len() == 0 {
			return
		}
		out = append(out, Message{
			Role:  RoleUser,
			Parts: []Part{{Kind: PartText, Text: results.String()}},
		})
		results.Reset()
	}

	for _, message := range messages {
		if message.Role == RoleTool {
			for _, part := range message.Parts {
				if part.Kind != PartToolResult {
					continue
				}
				// An empty result is a real answer — a tool that returned
				// nothing still returned — so this is not gated on text.
				results.WriteString(toolResponseOpen + "\n" +
					part.Text + "\n" + toolResponseClose + "\n")
			}
			continue
		}
		flush()
		if message.Role != RoleAssistant {
			out = append(out, message)
			continue
		}
		parts := make([]Part, 0, len(message.Parts))
		for _, part := range message.Parts {
			if part.Kind == PartToolCall && part.ToolName != "" {
				parts = append(parts, Part{
					Kind: PartText,
					Text: serializeEmittedCall(part.ToolName, part.ToolArgs),
				})
				continue
			}
			parts = append(parts, part)
		}
		out = append(out, Message{Role: RoleAssistant, Parts: parts})
	}
	flush()
	return out
}

// serializeEmittedCall writes one past call back the way the model would
// have written it, so a replayed transcript reads in one voice. Arguments
// that are not valid JSON become {} — they cost one call, where sending
// them raw would cost the whole request.
func serializeEmittedCall(name, args string) string {
	if strings.TrimSpace(args) == "" || !json.Valid([]byte(args)) {
		args = "{}"
	}
	return toolCallOpen + "\n" +
		`{"name":` + strconv.Quote(name) + `,"arguments":` + args + "}\n" +
		toolCallClose
}

func prependInstructions(messages []Message, block string) []Message {
	for i := range messages {
		if messages[i].Role != RoleUser {
			continue
		}
		parts := make([]Part, 0, len(messages[i].Parts)+1)
		parts = append(parts, Part{Kind: PartText, Text: block})
		parts = append(parts, messages[i].Parts...)
		messages[i].Parts = parts
		return messages
	}
	return append(messages, Message{
		Role:  RoleUser,
		Parts: []Part{{Kind: PartText, Text: block}},
	})
}

// toolInstructions is the prose that replaces the tools field. Written for a
// model that has never seen a tools array: the list, one worked example of
// the block, and what happens after it writes one.
func toolInstructions(tools []Tool, choice ToolChoice) string {
	var b strings.Builder
	b.WriteString("You can call tools. Each one is listed below as JSON, " +
		"with its name, what it is for, and the parameters it takes.\n\n<tools>\n")
	b.WriteString(promptToolList(tools))
	b.WriteString("\n</tools>\n\n")
	b.WriteString("To call a tool, write a block exactly like this, with the " +
		"tool's name and a JSON object of its arguments:\n\n")
	b.WriteString(toolCallOpen + "\n" +
		`{"name": "tool_name", "arguments": {"parameter": "value"}}` + "\n" +
		toolCallClose)
	b.WriteString("\n\nThe block stands on its own — not inside a code fence, " +
		"with nothing in it but the JSON. Write several blocks in one reply to " +
		"call several tools at once. The result of each call arrives in your " +
		"next message inside " + toolResponseOpen + " tags; read it and carry " +
		"on: answer, or call another tool. When no tool would help, reply with " +
		"no " + toolCallOpen + " block at all.")
	switch choice.Mode {
	case ToolChoiceRequired:
		b.WriteString(" You must call one of the tools in this reply.")
	case ToolChoiceNamed:
		b.WriteString(fmt.Sprintf(" You must call the tool %q in this reply.", choice.Name))
	}
	b.WriteString("\n")
	return b.String()
}

// promptToolList is the catalogue a model reads. The schema travels exactly
// as the caller declared it, for the same reason the adapters forward it
// untouched: it is the caller's contract with their own function.
func promptToolList(tools []Tool) string {
	type promptTool struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	}
	entries := make([]promptTool, 0, len(tools))
	for _, tool := range tools {
		schema := tool.Parameters
		if len(bytes.TrimSpace(schema)) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		entries = append(entries, promptTool{
			Name: tool.Name, Description: tool.Description, Parameters: schema,
		})
	}
	encoded, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

// --- the answer that comes back -------------------------------------------------

// toolCallFilter is the sink an emulated request is read through. Prose
// passes downstream as it arrives; a tool_call block is held whole until its
// closing tag, then either becomes a call or — when what is inside does not
// name a tool — flows downstream as the prose it turned out to be.
type toolCallFilter struct {
	sink  Sink
	prose strings.Builder
	// Text whose meaning is not decided yet: outside a block it is a
	// trailing fragment that could still grow into an opening tag; inside
	// one it is the block so far.
	pending  strings.Builder
	inside   bool
	sawDelta bool
	calls    []ToolCall
}

func (f *toolCallFilter) event(event Event) error {
	if event.Type == EventDelta {
		return f.delta(event.Text)
	}
	if f.sink == nil {
		return nil
	}
	return f.sink(event)
}

func (f *toolCallFilter) delta(text string) error {
	f.sawDelta = true
	f.pending.WriteString(text)
	for {
		buffered := f.pending.String()
		if !f.inside {
			open := strings.Index(buffered, toolCallOpen)
			if open < 0 {
				// A trailing "<tool_ca" may still become the opening tag, so
				// it is held back rather than shown and then taken back.
				hold := partialTagLen(buffered, toolCallOpen)
				release := buffered[:len(buffered)-hold]
				f.pending.Reset()
				f.pending.WriteString(buffered[len(buffered)-hold:])
				return f.emit(release)
			}
			if err := f.emit(buffered[:open]); err != nil {
				return err
			}
			f.pending.Reset()
			f.pending.WriteString(buffered[open+len(toolCallOpen):])
			f.inside = true
			continue
		}
		end := strings.Index(buffered, toolCallClose)
		if end < 0 {
			// Nothing is emitted from inside a block, so there is nothing to
			// hold back: the whole buffer stays until the tag arrives.
			return nil
		}
		body := buffered[:end]
		if call, ok := parseToolCallBody(body); ok {
			call.ID = fmt.Sprintf("call_%d", len(f.calls)+1)
			f.calls = append(f.calls, call)
		} else if err := f.emit(toolCallOpen + body + toolCallClose); err != nil {
			// Not a call after all — a reply that mentions the format, say.
			// What the model wrote stays visible; eating it would hide a
			// reply behind a guess about what it meant.
			return err
		}
		f.pending.Reset()
		f.pending.WriteString(buffered[end+len(toolCallClose):])
		f.inside = false
	}
}

func (f *toolCallFilter) emit(text string) error {
	if text == "" {
		return nil
	}
	f.prose.WriteString(text)
	if f.sink == nil {
		return nil
	}
	return f.sink(Event{Type: EventDelta, Text: text})
}

// finish settles what is left undecided and moves the assembled calls into
// the result — as events too, so a streaming caller has already seen
// everything a buffered one reads here.
func (f *toolCallFilter) finish(result *Result) error {
	// A buffered answer never reached the sink; run it through the same
	// machine so both paths decide prose identically.
	if !f.sawDelta && result.Text != "" {
		if err := f.delta(result.Text); err != nil {
			return err
		}
	}
	if f.inside {
		// The answer ended inside a block: either the length limit cut a
		// call in half, or the tag was prose that never closed. Both
		// readings are tried rather than one preferred.
		if call, ok := parseToolCallBody(f.pending.String()); ok {
			call.ID = fmt.Sprintf("call_%d", len(f.calls)+1)
			f.calls = append(f.calls, call)
		} else if err := f.emit(toolCallOpen + f.pending.String()); err != nil {
			return err
		}
	} else if err := f.emit(f.pending.String()); err != nil {
		// The held-back fragment never grew into a tag.
		return err
	}

	text := f.prose.String()
	// A turn that was nothing but calls has no text, rather than the
	// newline that surrounded the blocks.
	if strings.TrimSpace(text) == "" {
		text = ""
	}
	result.Text = text
	if len(f.calls) == 0 {
		return nil
	}
	result.ToolCalls = append(result.ToolCalls, f.calls...)
	if f.sink != nil {
		for _, call := range f.calls {
			if err := f.sink(Event{Type: EventToolCall, ToolCall: call}); err != nil {
				return err
			}
		}
	}
	// A client decides what to do next from this; "stop" would have an
	// agent treat a call as a final answer.
	if result.FinishReason == "" || strings.EqualFold(result.FinishReason, "stop") {
		result.FinishReason = "tool_calls"
	}
	return nil
}

// partialTagLen reports how many trailing bytes could still grow into tag.
func partialTagLen(text, tag string) int {
	for length := len(tag) - 1; length > 0; length-- {
		if strings.HasSuffix(text, tag[:length]) {
			return length
		}
	}
	return 0
}

// parseToolCallBody reads the inside of one tool_call block. It is lenient
// about spelling — a model imitating a format writes "parameters" for
// "arguments", nests the pair under "function" the way the wire formats it,
// and sometimes wraps the JSON in a code fence — and rejects anything that
// does not name a tool, which is what separates a call from prose that
// mentions the format.
func parseToolCallBody(body string) (ToolCall, bool) {
	body = stripCodeFence(strings.TrimSpace(body))
	if body == "" {
		return ToolCall{}, false
	}
	var read struct {
		Name       string          `json:"name"`
		Arguments  json.RawMessage `json:"arguments"`
		Parameters json.RawMessage `json:"parameters"`
		Function   *struct {
			Name       string          `json:"name"`
			Arguments  json.RawMessage `json:"arguments"`
			Parameters json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(body), &read); err != nil {
		return ToolCall{}, false
	}
	name, arguments := read.Name, firstRawJSON(read.Arguments, read.Parameters)
	if read.Function != nil {
		if name == "" {
			name = read.Function.Name
		}
		if len(arguments) == 0 {
			arguments = firstRawJSON(read.Function.Arguments, read.Function.Parameters)
		}
	}
	if name == "" {
		return ToolCall{}, false
	}
	return ToolCall{Name: name, Arguments: emulatedArguments(arguments)}, true
}

func firstRawJSON(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

// emulatedArguments is the arguments as the wire wants them: JSON text. A
// model that double-encoded its object — arguments as a JSON string holding
// JSON — is unwrapped one level, which is the spelling several models learn
// from examples that had to escape the quotes.
func emulatedArguments(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if json.Valid([]byte(asString)) {
			return asString
		}
		return string(raw)
	}
	return string(raw)
}

// stripCodeFence removes the markdown fence a model may have wrapped the
// JSON in, on the two lines where it would otherwise break the parse.
func stripCodeFence(body string) string {
	if !strings.HasPrefix(body, "```") {
		return body
	}
	end := strings.IndexByte(body, '\n')
	if end < 0 {
		return body
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(body[end+1:]), "```"))
}
