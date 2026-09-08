package adapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

const defaultAnthropicVersion = "2023-06-01"

// Budgets for the unified reasoning effort. Anthropic wants a token count,
// not a level, so the three levels have to become numbers somewhere; here is
// the only place in the server that knows they do.
const (
	budgetLow    = 2048
	budgetMedium = 8192
	budgetHigh   = 24576
	// The smallest budget the API accepts.
	budgetMin = 1024
	// The API requires max_tokens to exceed the thinking budget, and an
	// answer needs room after the reasoning.
	budgetHeadroom = 1024
	minBudget      = 1024
)

type anthropicAdapter struct{}

func (anthropicAdapter) Kind() Kind { return KindAnthropic }

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicImageBlock struct {
	Type   string `json:"type"`
	Source struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
}

type anthropicToolUseBlock struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
	// An object here, where the other protocol carries the same thing as
	// JSON text.
	Input json.RawMessage `json:"input"`
}

type anthropicToolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

func (a anthropicAdapter) buildBody(p Provider, req ChatRequest) (map[string]any, bool) {
	messages, carriedImages := a.buildMessages(req)

	body := map[string]any{
		"model":    req.Model.ModelID,
		"messages": messages,
	}

	thinking := req.Reasoning.Enabled && req.Model.SupportsReasoning &&
		resolveReasoningStyle(p) == ReasoningAnthropic

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = req.Model.MaxOutputTokens
	}
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	if thinking {
		budget := req.Reasoning.Budget
		if budget <= 0 {
			budget = reasoningBudget(req.Reasoning.Effort)
		}
		if budget < budgetMin {
			// The API refuses anything smaller, and a tier configured below
			// the floor should still think rather than fail the turn.
			budget = budgetMin
		}
		if budget+budgetHeadroom > maxTokens {
			// Prefer keeping the requested budget and raising the ceiling;
			// shrinking the budget instead would quietly downgrade what the
			// user asked for.
			maxTokens = budget + budgetHeadroom
		}
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
		// The API rejects a temperature other than 1 while thinking is on, so
		// the user's value is dropped rather than made into an error.
	} else if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}

	body["max_tokens"] = maxTokens

	if req.System != "" && req.Model.SupportsSystem {
		// Anthropic carries the system prompt beside the messages rather than
		// as one of them.
		body["system"] = req.System
	}

	if len(req.Tools) > 0 {
		body["tools"] = anthropicTools(req.Tools)
		// While thinking is on the API accepts only auto and none, so a
		// forced choice is dropped rather than made into a failed turn —
		// the same trade as the temperature above.
		if choice, ok := anthropicToolChoice(req.ToolChoice); ok &&
			(!thinking || req.ToolChoice.Mode == ToolChoiceNone) {
			body["tool_choice"] = choice
		}
	}

	for key, value := range req.Extra {
		body[key] = value
	}
	return body, carriedImages
}

func anthropicTools(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		entry := map[string]any{
			"name": tool.Name,
			// The same schema under the name this protocol gives the field.
			"input_schema": schemaOrEmpty(tool.Parameters),
		}
		if tool.Description != "" {
			entry["description"] = tool.Description
		}
		out = append(out, entry)
	}
	return out
}

func anthropicToolChoice(choice ToolChoice) (any, bool) {
	switch choice.Mode {
	case ToolChoiceNone:
		return map[string]any{"type": "none"}, true
	case ToolChoiceRequired:
		// "any" is this protocol's word for "call something".
		return map[string]any{"type": "any"}, true
	case ToolChoiceNamed:
		if choice.Name == "" {
			return nil, false
		}
		return map[string]any{"type": "tool", "name": choice.Name}, true
	}
	return nil, false
}

// toolInput is a call's arguments as this protocol wants them: an object.
//
// Anything that will not parse becomes an empty one. The arguments are
// already lost at that point, and sending them raw would put malformed JSON
// in the request body, which loses the whole turn instead of one call.
func toolInput(arguments string) json.RawMessage {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(trimmed)
}

func reasoningBudget(effort Effort) int {
	switch effort {
	case EffortLow:
		return budgetLow
	case EffortHigh:
		return budgetHigh
	default:
		return budgetMedium
	}
}

// buildMessages converts the unified message list, and repairs the two shapes
// Anthropic rejects outright: a list that does not start with a user turn,
// and two consecutive messages from the same role. Editing a message
// mid-transcript can produce either.
func (anthropicAdapter) buildMessages(req ChatRequest) ([]anthropicMessage, bool) {
	out := make([]anthropicMessage, 0, len(req.Messages))
	carriedImages := false

	for _, message := range req.Messages {
		var results, images, calls []any
		text := strings.Builder{}

		for _, part := range message.Parts {
			switch part.Kind {
			case PartText:
				if part.Text != "" {
					text.WriteString(part.Text)
				}
			case PartImage:
				if !req.Model.SupportsImages || len(part.Data) == 0 {
					continue
				}
				block := anthropicImageBlock{Type: "image"}
				block.Source.Type = "base64"
				block.Source.MediaType = part.MediaType
				block.Source.Data = base64.StdEncoding.EncodeToString(part.Data)
				images = append(images, block)
				carriedImages = true
			case PartToolCall:
				if part.ToolName == "" {
					continue
				}
				calls = append(calls, anthropicToolUseBlock{
					Type: "tool_use", ID: part.ToolCallID, Name: part.ToolName,
					Input: toolInput(part.ToolArgs),
				})
			case PartToolResult:
				if part.ToolCallID == "" {
					continue
				}
				results = append(results, anthropicToolResultBlock{
					Type: "tool_result", ToolUseID: part.ToolCallID, Content: part.Text,
				})
			}
		}

		// The order the API requires, and the order that reads best.
		//
		// Tool results head their turn — the API refuses a turn whose results
		// come after anything else. Text precedes the pictures it is about,
		// because the models follow an instruction better that way. Tool
		// calls come last, after whatever the model said about making them.
		blocks := make([]any, 0, len(results)+len(images)+len(calls)+1)
		blocks = append(blocks, results...)
		if text.Len() > 0 {
			blocks = append(blocks, anthropicTextBlock{Type: "text", Text: text.String()})
		}
		blocks = append(blocks, images...)
		blocks = append(blocks, calls...)
		if len(blocks) == 0 {
			continue
		}

		// A tool result is a user turn here, which is the whole reason the
		// unified layer keeps them apart: the other protocol gives them a
		// role of their own.
		role := "user"
		if message.Role == RoleAssistant {
			role = "assistant"
		}

		if last := len(out) - 1; last >= 0 && out[last].Role == role {
			merged := append(out[last].Content.([]any), blocks...)
			out[last].Content = merged
			continue
		}
		out = append(out, anthropicMessage{Role: role, Content: blocks})
	}

	for len(out) > 0 && out[0].Role != "user" {
		out = out[1:]
	}
	return out, carriedImages
}

func (a anthropicAdapter) Chat(ctx context.Context, client *http.Client, p Provider, req ChatRequest, sink Sink) (Result, error) {
	body, carriedImages := a.buildBody(p, req)
	endpoint := chatEndpoint(KindAnthropic, p.BaseURL)

	streaming := req.Stream && req.Model.SupportsStreaming
	if streaming {
		body["stream"] = true
	}

	response, err := postJSON(ctx, client, p, endpoint, body)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()

	if response.StatusCode >= 400 {
		return Result{}, classifyHTTP(p, endpoint, response.StatusCode,
			response.Header.Get("Retry-After"), readErrorBody(response), carriedImages)
	}

	if streaming && strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		return a.readStream(ctx, response, sink)
	}
	if streaming {
		// Asked for a stream and got a document. Rather than failing, the
		// whole answer is delivered at once and the interface is told why it
		// did not arrive progressively.
		result, err := a.readOnce(response)
		if err != nil {
			return Result{}, err
		}
		result.StreamFallbackReason = "the endpoint answered with " +
			fallbackContentType(response.Header.Get("Content-Type")) + " instead of an event stream"
		if err := emitAll(sink, result); err != nil {
			return Result{}, err
		}
		return result, nil
	}
	return a.readOnce(response)
}

func (anthropicAdapter) readOnce(response *http.Response) (Result, error) {
	var payload struct {
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			ID       string          `json:"id"`
			Name     string          `json:"name"`
			Input    json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return Result{}, &Error{Kind: ErrorUpstream, Message: "The provider returned a response we could not read.", cause: err}
	}
	if payload.StopReason == "refusal" {
		return Result{}, &Error{Kind: ErrorRefusal, Message: "The model declined to answer."}
	}

	var result Result
	for _, block := range payload.Content {
		switch block.Type {
		case "text":
			result.Text += block.Text
		case "thinking":
			result.Reasoning += block.Thinking
		case "tool_use":
			if block.Name == "" {
				continue
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID: block.ID, Name: block.Name,
				Arguments: toolArguments(string(block.Input)),
			})
		}
	}
	result.FinishReason = payload.StopReason
	result.Usage = Usage{InputTokens: payload.Usage.InputTokens, OutputTokens: payload.Usage.OutputTokens}
	return result, nil
}

type anthropicStreamEvent struct {
	Type string `json:"type"`
	// Which content block this frame belongs to. Tool calls are the reason it
	// matters: a model may open several at once and their argument fragments
	// interleave.
	Index        int `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
		// A tool call's arguments, a few characters at a time.
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Message struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (anthropicAdapter) readStream(ctx context.Context, response *http.Response, sink Sink) (Result, error) {
	result := Result{Streamed: true}
	var sinkErr error

	// A tool call is its own content block: a start frame naming it, then the
	// arguments as JSON fragments, then a stop. Each call is emitted at its
	// stop — the first moment the arguments are whole, and earlier than the
	// end of the message, so an agent can begin work while the model is
	// still writing the next one.
	pending := map[int]*ToolCall{}

	err := readEventStream(response.Body, func(data []byte) error {
		var event anthropicStreamEvent
		if err := json.Unmarshal(data, &event); err != nil {
			// A frame we cannot parse is skipped rather than fatal: a proxy
			// injecting a keepalive should not end a good generation.
			return nil
		}

		if event.Type == "error" {
			return &Error{Kind: ErrorUpstream, Message: firstNonEmpty(event.Error.Message, "The provider reported an error mid-stream.")}
		}

		// Anthropic reports input tokens when the message starts and output
		// tokens as it ends, so both have to be folded in rather than
		// overwriting one another.
		if usage := (Usage{InputTokens: event.Message.Usage.InputTokens, OutputTokens: event.Message.Usage.OutputTokens}); usage.Total() > 0 {
			result.Usage = result.Usage.Merge(usage)
			if err := sink(Event{Type: EventUsage, Usage: result.Usage}); err != nil {
				sinkErr = err
				return err
			}
		}
		if usage := (Usage{InputTokens: event.Usage.InputTokens, OutputTokens: event.Usage.OutputTokens}); usage.Total() > 0 {
			result.Usage = result.Usage.Merge(usage)
			if err := sink(Event{Type: EventUsage, Usage: result.Usage}); err != nil {
				sinkErr = err
				return err
			}
		}
		if event.Delta.StopReason != "" {
			result.FinishReason = event.Delta.StopReason
		}

		switch event.Type {
		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" && event.ContentBlock.Name != "" {
				pending[event.Index] = &ToolCall{
					ID: event.ContentBlock.ID, Name: event.ContentBlock.Name,
				}
			}
		case "content_block_stop":
			call, open := pending[event.Index]
			if !open {
				return nil
			}
			delete(pending, event.Index)
			call.Arguments = toolArguments(call.Arguments)
			result.ToolCalls = append(result.ToolCalls, *call)
			if err := sink(Event{Type: EventToolCall, ToolCall: *call}); err != nil {
				sinkErr = err
				return err
			}
			return nil
		}

		switch event.Delta.Type {
		case "text_delta":
			if event.Delta.Text == "" {
				return nil
			}
			result.Text += event.Delta.Text
			if err := sink(Event{Type: EventDelta, Text: event.Delta.Text}); err != nil {
				sinkErr = err
				return err
			}
		case "thinking_delta":
			if event.Delta.Thinking == "" {
				return nil
			}
			result.Reasoning += event.Delta.Thinking
			if err := sink(Event{Type: EventReasoning, Text: event.Delta.Thinking}); err != nil {
				sinkErr = err
				return err
			}
		case "input_json_delta":
			// A fragment for a call that never started, or one already
			// emitted, has nowhere to go. Both mean a frame arrived out of
			// order, which is not worth ending a good generation over.
			if call, open := pending[event.Index]; open {
				call.Arguments += event.Delta.PartialJSON
			}
		}
		return nil
	})

	if err != nil {
		// A sink failure is the caller's own error (a disconnected client),
		// and is returned unchanged so the gateway can tell it apart from an
		// upstream problem. Whatever streamed before it is kept.
		if sinkErr != nil {
			return result, sinkErr
		}
		var adapterErr *Error
		if ok := asAdapterError(err, &adapterErr); ok {
			return result, adapterErr
		}
		return result, networkError(ctx, err)
	}
	return result, nil
}
