package compat

// The Anthropic Messages API at /v1/messages.
//
// It exists because Claude Code speaks this protocol and only this protocol —
// there is no setting that makes it talk chat/completions — so an instance
// that serves only the OpenAI shape cannot be an agent's backend however
// complete that shape is. The same is true of anything else built on
// Anthropic's SDK.
//
// Everything the other surface promises holds here too: a key and only a key,
// no conversation written, the usage ledger through the same hooks, and
// nothing a provider said forwarded verbatim. What differs is only the
// spelling — blocks instead of messages, named SSE events instead of one
// chunk shape, and an error envelope of its own.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

type messagesRequest struct {
	Model     string            `json:"model"`
	Messages  []anthropicInMsg  `json:"messages"`
	System    json.RawMessage   `json:"system"`
	MaxTokens int               `json:"max_tokens"`
	Stream    *bool             `json:"stream"`
	Tools     []anthropicInTool `json:"tools"`
	// A string in no version of this protocol, but an object in every one.
	ToolChoice  json.RawMessage `json:"tool_choice"`
	Temperature *float64        `json:"temperature"`
	Thinking    *struct {
		Type         string `json:"type"`
		BudgetTokens int    `json:"budget_tokens"`
	} `json:"thinking"`
}

type anthropicInMsg struct {
	Role string `json:"role"`
	// A string, or an array of blocks. Both are current.
	Content json.RawMessage `json:"content"`
}

type anthropicInTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	// Set on the tools Anthropic hosts itself — a text editor, a bash
	// sandbox. Named so they can be refused rather than silently offered.
	Type string `json:"type"`
}

// One block of a message, in every shape this endpoint reads. Kept as one
// struct rather than a union because the fields do not collide and a union
// here would be three decodes of the same bytes.
type anthropicInBlock struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Source *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`

	// tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`

	// tool_result
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

func (h *Handlers) messages(w http.ResponseWriter, r *http.Request, who caller) error {
	var body messagesRequest
	if err := decode(w, r, &body); err != nil {
		return err
	}

	resolved, err := h.authorize(r, who, body.Model)
	if err != nil {
		return err
	}

	request, err := h.buildMessagesRequest(body, resolved)
	if err != nil {
		return err
	}

	release, err := h.reserve(r.Context(), who, resolved)
	if err != nil {
		return err
	}
	defer release()

	label := strings.TrimSpace(body.Model)
	if request.Stream {
		return h.messagesStreamed(w, r, who, request, resolved, label)
	}
	return h.messagesBuffered(w, r, who, request, resolved, label)
}

// --- request translation ------------------------------------------------------

func (h *Handlers) buildMessagesRequest(body messagesRequest, resolved model.Resolved) (adapter.ChatRequest, error) {
	if len(body.Messages) == 0 {
		return adapter.ChatRequest{}, anthropicBadRequest("At least one message is required.")
	}
	if len(body.Messages) > maxMessages {
		return adapter.ChatRequest{}, anthropicBadRequest("Too many messages in one request.")
	}

	messages := make([]adapter.Message, 0, len(body.Messages))
	for _, incoming := range body.Messages {
		blocks, err := readAnthropicContent(incoming.Content)
		if err != nil {
			return adapter.ChatRequest{}, err
		}
		if len(blocks) == 0 {
			continue
		}

		// A turn carrying results is a tool turn, whatever role it arrived
		// under: this protocol puts them in a user message, and the layer
		// below keeps the two apart because the other protocol does not.
		results, rest := splitToolResults(blocks)
		if len(results) > 0 {
			messages = append(messages, adapter.Message{Role: adapter.RoleTool, Parts: results})
		}
		if len(rest) == 0 {
			continue
		}
		role := adapter.RoleUser
		if strings.EqualFold(strings.TrimSpace(incoming.Role), "assistant") {
			role = adapter.RoleAssistant
		}
		messages = append(messages, adapter.Message{Role: role, Parts: rest})
	}

	if len(messages) == 0 {
		return adapter.ChatRequest{}, anthropicBadRequest("At least one message with content is required.")
	}

	prompt, err := readAnthropicSystem(body.System)
	if err != nil {
		return adapter.ChatRequest{}, err
	}
	if prompt == "" {
		prompt = resolved.Model.Prompt(h.settings.Get(settings.DefaultSystemPrompt))
	}

	tools, err := readAnthropicTools(body.Tools)
	if err != nil {
		return adapter.ChatRequest{}, err
	}
	choice, err := readAnthropicToolChoice(body.ToolChoice)
	if err != nil {
		return adapter.ChatRequest{}, err
	}

	reasoning := adapter.Reasoning{}
	if body.Thinking != nil && body.Thinking.Type == "enabled" &&
		resolved.Model.SupportsReasoning && resolved.Upstream.SupportsReasoning {
		reasoning = adapter.Reasoning{Enabled: true, Budget: body.Thinking.BudgetTokens}
	}

	// Absent means false here, as it does everywhere: this protocol's clients
	// send the field when they want a stream.
	stream := false
	if body.Stream != nil {
		stream = *body.Stream
	}

	return adapter.ChatRequest{
		Model:       resolved.Upstream.Spec(),
		System:      prompt,
		Messages:    messages,
		Temperature: body.Temperature,
		MaxTokens:   anthropicCeiling(resolved, body.MaxTokens),
		Reasoning:   reasoning,
		Stream:      stream,
		Tools:       tools,
		ToolChoice:  choice,
	}, nil
}

// splitToolResults pulls a turn's results out of its blocks. They travel as
// their own message because the two protocols disagree about whose turn a
// result belongs to, and the layer below is the one that knows.
func splitToolResults(blocks []adapter.Part) (results, rest []adapter.Part) {
	for _, block := range blocks {
		if block.Kind == adapter.PartToolResult {
			results = append(results, block)
			continue
		}
		rest = append(rest, block)
	}
	return results, rest
}

// anthropicCeiling is how many tokens the answer may run to. Unlike the
// upstream API this does not insist on max_tokens: a caller that leaves it
// out gets the model's own ceiling rather than a refusal.
func anthropicCeiling(resolved model.Resolved, asked int) int {
	limit := resolved.Upstream.MaxOutputTokens
	if limit == 0 {
		limit = resolved.Model.MaxOutputTokens
	}
	if asked <= 0 {
		return limit
	}
	if limit > 0 && asked > limit {
		return limit
	}
	return asked
}

func readAnthropicSystem(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	var blocks []anthropicInBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", anthropicBadRequest("system must be a string or an array of text blocks.")
	}
	var out strings.Builder
	for _, block := range blocks {
		if block.Type != "text" && block.Type != "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(block.Text)
	}
	return out.String(), nil
}

func readAnthropicContent(raw json.RawMessage) ([]adapter.Part, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		return []adapter.Part{{Kind: adapter.PartText, Text: text}}, nil
	}

	var blocks []anthropicInBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, anthropicBadRequest("Message content must be a string or an array of blocks.")
	}

	out := make([]adapter.Part, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text", "":
			if strings.TrimSpace(block.Text) != "" {
				out = append(out, adapter.Part{Kind: adapter.PartText, Text: block.Text})
			}
		case "image":
			part, err := readAnthropicImage(block)
			if err != nil {
				return nil, err
			}
			out = append(out, part)
		case "tool_use":
			if block.Name == "" {
				continue
			}
			out = append(out, adapter.Part{
				Kind:       adapter.PartToolCall,
				ToolCallID: block.ID,
				ToolName:   block.Name,
				ToolArgs:   strings.TrimSpace(string(block.Input)),
			})
		case "tool_result":
			if block.ToolUseID == "" {
				return nil, anthropicBadRequest("A tool_result block must name the call it answers in tool_use_id.")
			}
			nested, err := readAnthropicContent(block.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, adapter.Part{
				Kind:       adapter.PartToolResult,
				ToolCallID: block.ToolUseID,
				Text:       joinText(nested),
			})
		case "thinking", "redacted_thinking":
			// A model's own reasoning, replayed. It is dropped rather than
			// forwarded: it arrives with a signature this instance cannot
			// have produced and cannot verify, and sending one upstream that
			// does not check out fails the whole turn.
		default:
			return nil, anthropicBadRequest("Unsupported content block: " + block.Type + ".")
		}
	}
	return out, nil
}

func readAnthropicImage(block anthropicInBlock) (adapter.Part, error) {
	// Only inline bytes. A url source would have this server fetch an address
	// of the caller's choosing, which is the request forgery primitive the
	// other surface refuses for the same reason.
	if block.Source == nil || block.Source.Type != "base64" {
		return adapter.Part{}, anthropicBadRequest(
			"Images must be inline base64 sources; this server does not fetch remote images.")
	}
	if !strings.HasPrefix(block.Source.MediaType, "image/") {
		return adapter.Part{}, anthropicBadRequest("Only images may be sent as image blocks.")
	}
	data, err := base64.StdEncoding.DecodeString(block.Source.Data)
	if err != nil {
		return adapter.Part{}, anthropicBadRequest("Image data is not valid base64.")
	}
	return adapter.Part{Kind: adapter.PartImage, MediaType: block.Source.MediaType, Data: data}, nil
}

func readAnthropicTools(declared []anthropicInTool) ([]adapter.Tool, error) {
	if len(declared) == 0 {
		return nil, nil
	}
	if len(declared) > maxTools {
		return nil, anthropicBadRequest("Too many tools in one request.")
	}

	out := make([]adapter.Tool, 0, len(declared))
	for _, tool := range declared {
		// A `type` other than custom names one of the tools the vendor runs
		// on its own side — a text editor, a bash sandbox, a web search.
		// Dropped rather than refused: this server cannot run one, a model
		// never told about it cannot call it, and refusing would stop a
		// client that offers one by default from starting at all.
		if tool.Type != "" && !strings.HasPrefix(tool.Type, "custom") {
			continue
		}
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return nil, anthropicBadRequest("Every tool needs a name.")
		}
		out = append(out, adapter.Tool{
			Name: name, Description: tool.Description, Parameters: tool.InputSchema,
		})
	}
	return out, nil
}

func readAnthropicToolChoice(raw json.RawMessage) (adapter.ToolChoice, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return adapter.ToolChoice{}, nil
	}

	var choice struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return adapter.ToolChoice{}, anthropicBadRequest("tool_choice must be an object naming a type.")
	}

	switch choice.Type {
	case "auto", "":
		return adapter.ToolChoice{}, nil
	case "none":
		return adapter.ToolChoice{Mode: adapter.ToolChoiceNone}, nil
	case "any":
		return adapter.ToolChoice{Mode: adapter.ToolChoiceRequired}, nil
	case "tool":
		if choice.Name == "" {
			return adapter.ToolChoice{}, anthropicBadRequest("tool_choice of type tool must name the tool.")
		}
		return adapter.ToolChoice{Mode: adapter.ToolChoiceNamed, Name: choice.Name}, nil
	}
	return adapter.ToolChoice{}, anthropicBadRequest("Unknown tool_choice type: " + choice.Type + ".")
}

// --- the buffered shape -------------------------------------------------------

func (h *Handlers) messagesBuffered(
	w http.ResponseWriter, r *http.Request, who caller,
	request adapter.ChatRequest, resolved model.Resolved, label string,
) error {
	startedAt := time.Now()
	requestID := id.New()

	var answer strings.Builder
	usage := adapter.Usage{}
	var calls []adapter.ToolCall

	sink := func(event adapter.Event) error {
		switch event.Type {
		case adapter.EventDelta:
			answer.WriteString(event.Text)
		case adapter.EventToolCall:
			calls = append(calls, event.ToolCall)
		case adapter.EventUsage:
			usage = usage.Merge(event.Usage)
		}
		return nil
	}

	result, chatErr := h.registry.Chat(r.Context(), resolved.Provider, request, sink)
	usage = usage.Merge(result.Usage)
	if result.Text != "" && answer.Len() == 0 {
		answer.WriteString(result.Text)
	}
	if len(calls) == 0 {
		calls = result.ToolCalls
	}

	if chatErr != nil {
		h.record(r.Context(), who, resolved, requestID, usage, startedAt, chatErr)
		return translateUpstream(chatErr)
	}
	h.record(r.Context(), who, resolved, requestID, usage, startedAt, nil)

	return writeJSON(w, http.StatusOK, anthropicMessage{
		ID:         messageID(requestID),
		Type:       "message",
		Role:       "assistant",
		Model:      label,
		Content:    anthropicContent(answer.String(), calls, requestID),
		StopReason: anthropicStopReason(result.FinishReason, len(calls)),
		Usage:      anthropicUsage(usage),
	})
}

// --- the streamed shape -------------------------------------------------------

// messagesStreamed answers in this protocol's event stream.
//
// Its shape is more prescribed than the other surface's: every piece of the
// answer is a numbered content block that must be opened, filled and closed,
// and the client tracks those indices. Text is one block; each tool call is
// another, opened and closed around the arguments it carries.
func (h *Handlers) messagesStreamed(
	w http.ResponseWriter, r *http.Request, who caller,
	request adapter.ChatRequest, resolved model.Resolved, label string,
) error {
	startedAt := time.Now()
	requestID := id.New()

	sse, err := httpx.NewSSE(w)
	if err != nil {
		return internalError(err)
	}

	usage := adapter.Usage{}
	var calls []adapter.ToolCall

	// Which block is being written, and whether the text one was ever
	// opened. A block is only opened when there is something to put in it,
	// because an empty text block is one the client renders as a blank turn.
	index := 0
	textOpen := false

	_ = sse.Event("message_start", map[string]any{
		"type": "message_start",
		"message": anthropicMessage{
			ID: messageID(requestID), Type: "message", Role: "assistant",
			Model: label, Content: []anthropicOutBlock{},
			Usage: &anthropicUsageBlock{},
		},
	})

	closeText := func() {
		if !textOpen {
			return
		}
		_ = sse.Event("content_block_stop", map[string]any{
			"type": "content_block_stop", "index": index,
		})
		textOpen = false
		index++
	}

	sink := func(event adapter.Event) error {
		switch event.Type {
		case adapter.EventDelta:
			if !textOpen {
				if err := sse.Event("content_block_start", map[string]any{
					"type": "content_block_start", "index": index,
					"content_block": map[string]any{"type": "text", "text": ""},
				}); err != nil {
					return err
				}
				textOpen = true
			}
			return sse.Event("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": index,
				"delta": map[string]any{"type": "text_delta", "text": event.Text},
			})

		case adapter.EventToolCall:
			// A call cannot share a block with the text before it, so
			// whatever is open closes first.
			closeText()
			calls = append(calls, event.ToolCall)
			return streamToolUse(sse, event.ToolCall, requestID, len(calls)-1, &index)

		case adapter.EventUsage:
			usage = usage.Merge(event.Usage)

		case adapter.EventReasoning:
			// Dropped: a thinking block travels with a signature that only
			// the model that produced it can mint, and one without is worse
			// than none — the client replays it on the next turn and the
			// upstream refuses the lot.
		}
		return nil
	}

	result, chatErr := h.registry.Chat(r.Context(), resolved.Provider, request, sink)
	usage = usage.Merge(result.Usage)

	// A provider that could not stream answered all at once, and the answer
	// still has to reach the client as blocks.
	if result.Text != "" && !textOpen && len(calls) == 0 {
		_ = sse.Event("content_block_start", map[string]any{
			"type": "content_block_start", "index": index,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		textOpen = true
		_ = sse.Event("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": index,
			"delta": map[string]any{"type": "text_delta", "text": result.Text},
		})
	}
	if len(calls) == 0 && len(result.ToolCalls) > 0 {
		closeText()
		for position, call := range result.ToolCalls {
			calls = append(calls, call)
			_ = streamToolUse(sse, call, requestID, position, &index)
		}
	}
	closeText()

	h.record(context.WithoutCancel(r.Context()), who, resolved, requestID, usage, startedAt, chatErr)

	if chatErr != nil {
		if r.Context().Err() != nil {
			return nil
		}
		rendered := translateUpstream(chatErr)
		_ = sse.Event("error", map[string]any{
			"type":  "error",
			"error": map[string]any{"type": anthropicErrorType(rendered.status), "message": rendered.message},
		})
		return nil
	}

	_ = sse.Event("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   anthropicStopReason(result.FinishReason, len(calls)),
			"stop_sequence": nil,
		},
		"usage": map[string]any{"output_tokens": usage.OutputTokens},
	})
	_ = sse.Event("message_stop", map[string]any{"type": "message_stop"})
	return nil
}

// streamToolUse writes one call as its own content block: opened with the id
// and name, filled with the arguments in one delta, closed. `at` is advanced
// past the block this used.
func streamToolUse(
	sse *httpx.SSE, call adapter.ToolCall, requestID string, position int, at *int,
) error {
	index := *at
	if err := sse.Event("content_block_start", map[string]any{
		"type": "content_block_start", "index": index,
		"content_block": map[string]any{
			"type": "tool_use", "id": callID(requestID, position),
			"name": call.Name, "input": map[string]any{},
		},
	}); err != nil {
		return err
	}
	// One delta rather than fragments, for the reason the adapters assemble
	// calls before handing them up: half an arguments object is not something
	// a client can do anything with.
	if err := sse.Event("content_block_delta", map[string]any{
		"type": "content_block_delta", "index": index,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": call.Arguments},
	}); err != nil {
		return err
	}
	if err := sse.Event("content_block_stop", map[string]any{
		"type": "content_block_stop", "index": index,
	}); err != nil {
		return err
	}
	*at = index + 1
	return nil
}

// --- response shapes ----------------------------------------------------------

type anthropicMessage struct {
	ID      string              `json:"id"`
	Type    string              `json:"type"`
	Role    string              `json:"role"`
	Model   string              `json:"model"`
	Content []anthropicOutBlock `json:"content"`
	// A pointer so an opening message renders it as null, which is what says
	// the turn is not finished.
	StopReason   *string              `json:"stop_reason"`
	StopSequence *string              `json:"stop_sequence"`
	Usage        *anthropicUsageBlock `json:"usage,omitempty"`
}

type anthropicOutBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthropicUsageBlock struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func anthropicUsage(u adapter.Usage) *anthropicUsageBlock {
	return &anthropicUsageBlock{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}
}

func anthropicContent(text string, calls []adapter.ToolCall, requestID string) []anthropicOutBlock {
	out := make([]anthropicOutBlock, 0, len(calls)+1)
	if text != "" {
		out = append(out, anthropicOutBlock{Type: "text", Text: text})
	}
	for position, call := range calls {
		out = append(out, anthropicOutBlock{
			Type: "tool_use", ID: callID(requestID, position),
			Name: call.Name, Input: json.RawMessage(call.Arguments),
		})
	}
	// The protocol has no empty content, and a client that gets one has
	// nothing to render.
	if len(out) == 0 {
		out = append(out, anthropicOutBlock{Type: "text", Text: ""})
	}
	return out
}

// anthropicStopReason renders the end of a turn in this protocol's words.
//
// Constructed rather than passed through, the same as on the other surface: a
// provider's own vocabulary would tell the caller which family answered a
// model the operator presented under their own name.
func anthropicStopReason(upstream string, toolCalls int) *string {
	reason := "end_turn"
	switch strings.ToLower(strings.TrimSpace(upstream)) {
	case "length", "max_tokens", "model_length":
		reason = "max_tokens"
	case "tool_calls", "tool_use", "function_call":
		reason = "tool_use"
	case "content_filter", "refusal":
		// This protocol has no word for a filter, and end_turn is what its
		// own API answers when a turn simply stopped.
		reason = "end_turn"
	}
	if toolCalls > 0 && reason == "end_turn" {
		reason = "tool_use"
	}
	return &reason
}

func messageID(requestID string) string { return "msg_" + requestID }

// --- counting -----------------------------------------------------------------

// countTokens answers /v1/messages/count_tokens, which agent clients call to
// decide when to compact a conversation.
//
// It is an estimate and cannot be anything else. A real count needs the
// tokeniser of the model that will answer, which differs per family, and
// carrying one would be both a dependency this project does not take and a
// second thing to keep in step with every model an operator adds. Four bytes
// to the token is the usual approximation and is within a fifth or so for
// English and for code.
//
// A 404 was the alternative, and it is worse: a client that cannot ask gets
// no number at all, where this one gets a number that is close. Being off by
// a fifth moves when it compacts; having none moves whether it can.
func (h *Handlers) countTokens(w http.ResponseWriter, r *http.Request, who caller) error {
	var body messagesRequest
	if err := decode(w, r, &body); err != nil {
		return err
	}

	resolved, err := h.authorize(r, who, body.Model)
	if err != nil {
		return err
	}
	request, err := h.buildMessagesRequest(body, resolved)
	if err != nil {
		return err
	}

	return writeJSON(w, http.StatusOK, map[string]any{
		"input_tokens": estimateTokens(request),
	})
}

// The divisor above, and a small allowance for the framing every message and
// every block carries on the wire.
const (
	bytesPerToken   = 4
	perMessageTax   = 4
	perToolCallTax  = 8
	imageTokenGuess = 1600
)

func estimateTokens(request adapter.ChatRequest) int {
	bytes := len(request.System)

	for _, tool := range request.Tools {
		// An agent's tool definitions are often most of its prompt, so
		// leaving them out would understate the total badly.
		bytes += len(tool.Name) + len(tool.Description) + len(tool.Parameters)
	}

	tokens := 0
	for _, message := range request.Messages {
		tokens += perMessageTax
		for _, part := range message.Parts {
			switch part.Kind {
			case adapter.PartImage:
				// Rather than the bytes, which are base64 and say nothing
				// about how the model will see the picture.
				tokens += imageTokenGuess
			case adapter.PartToolCall:
				bytes += len(part.ToolName) + len(part.ToolArgs)
				tokens += perToolCallTax
			default:
				bytes += len(part.Text) + len(part.ToolCallID)
			}
		}
	}

	return tokens + bytes/bytesPerToken
}

// --- errors -------------------------------------------------------------------

func anthropicBadRequest(message string) apiError {
	return apiError{
		status:  http.StatusBadRequest,
		kind:    "invalid_request_error",
		code:    "invalid_request_error",
		message: message,
	}
}

// anthropicErrorType maps a decided status onto this protocol's small fixed
// set, which is what its clients branch on.
func anthropicErrorType(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusServiceUnavailable, http.StatusBadGateway:
		return "overloaded_error"
	}
	if status >= 500 {
		return "api_error"
	}
	return "invalid_request_error"
}

// writeAnthropicError renders a failure in this protocol's envelope. The
// message is the same sentence the other surface would have shown; only the
// wrapper differs.
func writeAnthropicError(w http.ResponseWriter, err error) {
	var rendered apiError
	if !asAPIError(err, &rendered) {
		rendered = internalError(err)
	}
	if rendered.status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	}
	_ = writeJSON(w, rendered.status, map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    anthropicErrorType(rendered.status),
			"message": rendered.message,
		},
	})
}
