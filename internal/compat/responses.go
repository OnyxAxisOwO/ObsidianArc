package compat

// The OpenAI Responses API at /v1/responses.
//
// It exists for the same reason the Anthropic surface does: Codex removed
// `wire_api = "chat"` and now refuses to start against a chat/completions
// endpoint at all, so an instance without this cannot be its backend however
// complete the other two surfaces are.
//
// It is the same translation as the others and none of the protocol's own
// ambition. There is no `store`, no `previous_response_id` and no background
// mode, because all three mean the server keeps the conversation — and this
// package's first promise is that it keeps nothing. A client that asks for
// one is told so rather than silently answered as though its transcript had
// been remembered.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

type responsesRequest struct {
	Model string `json:"model"`
	// A bare string, or the array of items that carries a whole transcript.
	Input        json.RawMessage `json:"input"`
	Instructions string          `json:"instructions"`
	Stream       *bool           `json:"stream"`

	// This protocol's name for the ceiling. `max_tokens` is not read: it
	// means something else here and no client sends it.
	MaxOutputTokens *int     `json:"max_output_tokens"`
	Temperature     *float64 `json:"temperature"`

	Tools      []responsesTool `json:"tools"`
	ToolChoice json.RawMessage `json:"tool_choice"`

	Reasoning *struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`

	// Read only to be refused: both mean the server is keeping the
	// conversation, and this one does not.
	Store              *bool  `json:"store"`
	PreviousResponseID string `json:"previous_response_id"`
}

// responsesTool is flat, unlike the chat/completions shape that nests the
// same fields under `function`. Same tool, different spelling.
type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	// A namespace is a group rather than a tool: the functions inside it are
	// what the model can call. Codex sends one for its sub-agent tools.
	Tools []responsesTool `json:"tools"`
}

// One item of the input array, in every shape this endpoint reads.
type responsesItem struct {
	Type string `json:"type"`
	Role string `json:"role"`
	// A string, or an array of typed parts.
	Content json.RawMessage `json:"content"`

	// function_call
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`

	// function_call_output
	Output json.RawMessage `json:"output"`
}

type responsesPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL string `json:"image_url"`
}

func (h *Handlers) responses(w http.ResponseWriter, r *http.Request, who caller) error {
	var body responsesRequest
	if err := decode(w, r, &body); err != nil {
		return err
	}

	// Said plainly rather than answered as though the transcript had been
	// remembered, which would drop everything before this turn.
	if strings.TrimSpace(body.PreviousResponseID) != "" {
		return badRequest("previous_response_id",
			"This server keeps no conversations. Send the whole transcript in input, and set store to false.")
	}
	if body.Store != nil && *body.Store {
		return badRequest("store",
			"This server keeps no conversations. Set store to false.")
	}

	resolved, err := h.authorize(r, who, body.Model)
	if err != nil {
		return err
	}

	request, err := h.buildResponsesRequest(body, resolved)
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
		return h.responsesStreamed(w, r, who, request, resolved, label)
	}
	return h.responsesBuffered(w, r, who, request, resolved, label)
}

// --- request translation ------------------------------------------------------

func (h *Handlers) buildResponsesRequest(body responsesRequest, resolved model.Resolved) (adapter.ChatRequest, error) {
	items, err := readResponsesInput(body.Input)
	if err != nil {
		return adapter.ChatRequest{}, err
	}
	if len(items) == 0 {
		return adapter.ChatRequest{}, badRequest("input", "At least one input item is required.")
	}
	if len(items) > maxMessages {
		return adapter.ChatRequest{}, badRequest("input", "Too many input items in one request.")
	}

	var system strings.Builder
	if body.Instructions != "" {
		system.WriteString(body.Instructions)
	}

	messages := make([]adapter.Message, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "function_call":
			if item.Name == "" {
				continue
			}
			messages = append(messages, adapter.Message{
				Role: adapter.RoleAssistant,
				Parts: []adapter.Part{{
					Kind:       adapter.PartToolCall,
					ToolCallID: item.CallID,
					ToolName:   item.Name,
					ToolArgs:   item.Arguments,
				}},
			})

		case "function_call_output":
			if item.CallID == "" {
				return adapter.ChatRequest{}, badRequest("input",
					"A function_call_output must name the call it answers in call_id.")
			}
			messages = append(messages, adapter.Message{
				Role: adapter.RoleTool,
				Parts: []adapter.Part{{
					Kind:       adapter.PartToolResult,
					ToolCallID: item.CallID,
					Text:       readResponsesOutput(item.Output),
				}},
			})

		case "reasoning":
			// The model's own thinking, replayed. Dropped for the reason the
			// Anthropic surface drops it: what it carries is an encrypted
			// payload only its author can read.

		case "message", "":
			parts, err := readResponsesContent(item.Content)
			if err != nil {
				return adapter.ChatRequest{}, err
			}
			switch strings.ToLower(strings.TrimSpace(item.Role)) {
			case "system", "developer":
				// Hoisted, as on the other surfaces: the adapters take the
				// system prompt as its own field.
				if text := joinText(parts); text != "" {
					if system.Len() > 0 {
						system.WriteString("\n\n")
					}
					system.WriteString(text)
				}
			case "assistant":
				if len(parts) > 0 {
					messages = append(messages, adapter.Message{Role: adapter.RoleAssistant, Parts: parts})
				}
			default:
				if len(parts) > 0 {
					messages = append(messages, adapter.Message{Role: adapter.RoleUser, Parts: parts})
				}
			}

		default:
			return adapter.ChatRequest{}, badRequest("input", "Unsupported input item: "+item.Type+".")
		}
	}

	if len(messages) == 0 {
		return adapter.ChatRequest{}, badRequest("input", "At least one message is required.")
	}

	prompt := system.String()
	if prompt == "" {
		prompt = resolved.Model.Prompt(h.settings.Get(settings.DefaultSystemPrompt))
	}

	tools, err := readResponsesTools(body.Tools)
	if err != nil {
		return adapter.ChatRequest{}, err
	}
	choice, err := readToolChoice(body.ToolChoice)
	if err != nil {
		return adapter.ChatRequest{}, err
	}

	reasoning := adapter.Reasoning{}
	if body.Reasoning != nil {
		if effort, ok := parseEffort(body.Reasoning.Effort); ok &&
			resolved.Model.SupportsReasoning && resolved.Upstream.SupportsReasoning {
			reasoning = adapter.Reasoning{Enabled: true, Effort: effort}
		}
	}

	stream := false
	if body.Stream != nil {
		stream = *body.Stream
	}

	return adapter.ChatRequest{
		Model:       resolved.Upstream.Spec(),
		System:      prompt,
		Messages:    messages,
		Temperature: body.Temperature,
		MaxTokens:   responsesCeiling(resolved, body.MaxOutputTokens),
		Reasoning:   reasoning,
		Stream:      stream,
		Tools:       tools,
		ToolChoice:  choice,
	}, nil
}

func responsesCeiling(resolved model.Resolved, asked *int) int {
	limit := resolved.Upstream.MaxOutputTokens
	if limit == 0 {
		limit = resolved.Model.MaxOutputTokens
	}
	if asked == nil || *asked <= 0 {
		return limit
	}
	if limit > 0 && *asked > limit {
		return limit
	}
	return *asked
}

// readResponsesInput accepts both shapes the field takes: one string, which
// is the whole prompt, or the array of items that carries a transcript.
func readResponsesInput(raw json.RawMessage) ([]responsesItem, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		content, _ := json.Marshal(text)
		return []responsesItem{{Type: "message", Role: "user", Content: content}}, nil
	}

	var items []responsesItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, badRequest("input", "input must be a string or an array of items.")
	}
	return items, nil
}

func readResponsesContent(raw json.RawMessage) ([]adapter.Part, error) {
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

	var parts []responsesPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, badRequest("input", "Item content must be a string or an array of parts.")
	}

	out := make([]adapter.Part, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		// output_text appears on an assistant turn the client is replaying;
		// the rest are what a caller sends.
		case "input_text", "output_text", "text", "":
			if strings.TrimSpace(part.Text) != "" {
				out = append(out, adapter.Part{Kind: adapter.PartText, Text: part.Text})
			}
		case "input_image", "image_url":
			if part.ImageURL == "" {
				continue
			}
			image, err := decodeDataURL(part.ImageURL)
			if err != nil {
				return nil, err
			}
			out = append(out, image)
		case "refusal":
			// The model declining, replayed. It is text on this side and has
			// nowhere to go on the other.
		default:
			return nil, badRequest("input", "Unsupported content part: "+part.Type+".")
		}
	}
	return out, nil
}

// A function_call_output's payload is a string in every client, and an array
// of parts in the newest schema. Both flatten to the one string the layer
// below carries.
func readResponsesOutput(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	parts, err := readResponsesContent(raw)
	if err != nil {
		// Not a refusal: an output this layer cannot read is still an answer
		// the model should see, and the raw JSON is closer to it than
		// nothing.
		return trimmed
	}
	return joinText(parts)
}

func readResponsesTools(declared []responsesTool) ([]adapter.Tool, error) {
	if len(declared) == 0 {
		return nil, nil
	}

	out := make([]adapter.Tool, 0, len(declared))
	var walk func(tools []responsesTool, depth int) error
	walk = func(tools []responsesTool, depth int) error {
		for _, tool := range tools {
			switch tool.Type {
			case "function", "":
				name := strings.TrimSpace(tool.Name)
				if name == "" {
					return badRequest("tools", "Every tool needs a name.")
				}
				out = append(out, adapter.Tool{
					Name: name, Description: tool.Description, Parameters: tool.Parameters,
				})
			case "namespace":
				// Flattened to the functions inside, which carry their own
				// names and are what the model actually calls. A bound on
				// the depth because the shape is recursive and a body is not
				// a place to accept unbounded recursion from.
				if depth >= 4 {
					return badRequest("tools", "Tool namespaces are nested too deeply.")
				}
				if err := walk(tool.Tools, depth+1); err != nil {
					return err
				}
			default:
				// A tool the client's own vendor runs: a web search, a code
				// interpreter, a hosted container. Dropped rather than
				// refused — this server cannot run one, and a model never
				// told about it cannot call it, while refusing would stop an
				// agent that offers one by default from starting at all.
				// Codex sends web_search on every request.
			}
		}
		return nil
	}

	if err := walk(declared, 0); err != nil {
		return nil, err
	}
	if len(out) > maxTools {
		return nil, badRequest("tools", "Too many tools in one request.")
	}
	return out, nil
}

// --- the buffered shape -------------------------------------------------------

func (h *Handlers) responsesBuffered(
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

	return writeJSON(w, http.StatusOK, responseObject(
		requestID, label, startedAt, "completed",
		responsesOutput(requestID, answer.String(), calls), usage,
	))
}

// --- the streamed shape -------------------------------------------------------

// responsesStreamed answers in this protocol's event stream.
//
// Its unit is the output item rather than the content block: text is one
// item, each call is another, and a client learns what an item finally was
// from the `done` event that closes it. Codex builds its whole turn from
// those and from `response.completed`, so those are the events that must be
// right even when the deltas in between are not read.
func (h *Handlers) responsesStreamed(
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
	var answer strings.Builder

	// Every event carries its position in the stream, which is what a client
	// resuming or reordering them counts on.
	sequence := 0
	emit := func(name string, payload map[string]any) error {
		payload["type"] = name
		payload["sequence_number"] = sequence
		sequence++
		return sse.Event(name, payload)
	}

	index := 0
	textOpen := false
	textItemID := "msg_" + requestID

	_ = emit("response.created", map[string]any{
		"response": responseObject(requestID, label, startedAt, "in_progress", nil, adapter.Usage{}),
	})
	_ = emit("response.in_progress", map[string]any{
		"response": responseObject(requestID, label, startedAt, "in_progress", nil, adapter.Usage{}),
	})

	openText := func() error {
		if textOpen {
			return nil
		}
		textOpen = true
		if err := emit("response.output_item.added", map[string]any{
			"output_index": index,
			"item": map[string]any{
				"type": "message", "id": textItemID, "status": "in_progress",
				"role": "assistant", "content": []any{},
			},
		}); err != nil {
			return err
		}
		return emit("response.content_part.added", map[string]any{
			"item_id": textItemID, "output_index": index, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
		})
	}

	closeText := func() error {
		if !textOpen {
			return nil
		}
		textOpen = false
		if err := emit("response.output_text.done", map[string]any{
			"item_id": textItemID, "output_index": index, "content_index": 0,
			"text": answer.String(),
		}); err != nil {
			return err
		}
		if err := emit("response.content_part.done", map[string]any{
			"item_id": textItemID, "output_index": index, "content_index": 0,
			"part": map[string]any{
				"type": "output_text", "text": answer.String(), "annotations": []any{},
			},
		}); err != nil {
			return err
		}
		if err := emit("response.output_item.done", map[string]any{
			"output_index": index,
			"item":         textItem(textItemID, answer.String()),
		}); err != nil {
			return err
		}
		index++
		return nil
	}

	sendCall := func(call adapter.ToolCall, position int) error {
		if err := closeText(); err != nil {
			return err
		}
		item := callItem(requestID, call, position)
		opening := map[string]any{}
		for key, value := range item {
			opening[key] = value
		}
		opening["arguments"] = ""
		opening["status"] = "in_progress"

		if err := emit("response.output_item.added", map[string]any{
			"output_index": index, "item": opening,
		}); err != nil {
			return err
		}
		if err := emit("response.function_call_arguments.delta", map[string]any{
			"item_id": item["id"], "output_index": index, "delta": call.Arguments,
		}); err != nil {
			return err
		}
		if err := emit("response.function_call_arguments.done", map[string]any{
			"item_id": item["id"], "output_index": index, "arguments": call.Arguments,
		}); err != nil {
			return err
		}
		if err := emit("response.output_item.done", map[string]any{
			"output_index": index, "item": item,
		}); err != nil {
			return err
		}
		index++
		return nil
	}

	sink := func(event adapter.Event) error {
		switch event.Type {
		case adapter.EventDelta:
			if err := openText(); err != nil {
				return err
			}
			answer.WriteString(event.Text)
			return emit("response.output_text.delta", map[string]any{
				"item_id": textItemID, "output_index": index, "content_index": 0,
				"delta": event.Text,
			})
		case adapter.EventToolCall:
			calls = append(calls, event.ToolCall)
			return sendCall(event.ToolCall, len(calls)-1)
		case adapter.EventUsage:
			usage = usage.Merge(event.Usage)
		}
		return nil
	}

	result, chatErr := h.registry.Chat(r.Context(), resolved.Provider, request, sink)
	usage = usage.Merge(result.Usage)

	// A provider that could not stream answered all at once, and the answer
	// still has to reach the client as items.
	if result.Text != "" && answer.Len() == 0 {
		_ = openText()
		answer.WriteString(result.Text)
		_ = emit("response.output_text.delta", map[string]any{
			"item_id": textItemID, "output_index": index, "content_index": 0,
			"delta": result.Text,
		})
	}
	if len(calls) == 0 {
		for position, call := range result.ToolCalls {
			calls = append(calls, call)
			_ = sendCall(call, position)
		}
	}
	_ = closeText()

	h.record(context.WithoutCancel(r.Context()), who, resolved, requestID, usage, startedAt, chatErr)

	if chatErr != nil {
		if r.Context().Err() != nil {
			return nil
		}
		rendered := translateUpstream(chatErr)
		_ = emit("response.failed", map[string]any{
			"response": map[string]any{
				"id": responseID(requestID), "object": "response", "status": "failed",
				"error": map[string]any{"code": rendered.code, "message": rendered.message},
			},
		})
		return nil
	}

	_ = emit("response.completed", map[string]any{
		"response": responseObject(requestID, label, startedAt, "completed",
			responsesOutput(requestID, answer.String(), calls), usage),
	})
	return nil
}

// --- response shapes ----------------------------------------------------------

// responseObject is the envelope, which this protocol repeats in full on
// several events rather than sending once.
func responseObject(
	requestID, label string, startedAt time.Time,
	status string, output []map[string]any, usage adapter.Usage,
) map[string]any {
	if output == nil {
		output = []map[string]any{}
	}
	object := map[string]any{
		"id":         responseID(requestID),
		"object":     "response",
		"created_at": startedAt.Unix(),
		"status":     status,
		"model":      label,
		"output":     output,
		"error":      nil,
		// Named because a client reading a finished response looks for them,
		// and an absent field is not the same as a stated null.
		"incomplete_details":  nil,
		"instructions":        nil,
		"parallel_tool_calls": true,
		"tool_choice":         "auto",
		"tools":               []any{},
	}
	if usage.Total() > 0 {
		object["usage"] = map[string]any{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
			"total_tokens":  usage.Total(),
		}
	}
	return object
}

func responsesOutput(requestID, text string, calls []adapter.ToolCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls)+1)
	if text != "" {
		out = append(out, textItem("msg_"+requestID, text))
	}
	for position, call := range calls {
		out = append(out, callItem(requestID, call, position))
	}
	return out
}

func textItem(itemID, text string) map[string]any {
	return map[string]any{
		"type": "message", "id": itemID, "status": "completed", "role": "assistant",
		"content": []map[string]any{{
			"type": "output_text", "text": text, "annotations": []any{},
		}},
	}
}

func callItem(requestID string, call adapter.ToolCall, position int) map[string]any {
	// `id` names the item and `call_id` names the call the client answers.
	// They are distinct fields in this protocol and a client that confuses
	// them addresses its result at nothing.
	return map[string]any{
		"type":      "function_call",
		"id":        "fc_" + requestID + "_" + strconv.Itoa(position),
		"call_id":   callID(requestID, position),
		"name":      call.Name,
		"arguments": call.Arguments,
		"status":    "completed",
	}
}

func responseID(requestID string) string { return "resp_" + requestID }
