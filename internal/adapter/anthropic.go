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

func (anthropicAdapter) ListModels(ctx context.Context, client *http.Client, p Provider) ([]RemoteModel, error) {
	return listModels(ctx, client, p)
}

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
	IsError   bool   `json:"is_error,omitempty"`
}

func (a anthropicAdapter) buildBody(p Provider, req ChatRequest) (map[string]any, bool) {
	messages, carriedImages := a.buildMessages(req)

	body := map[string]any{
		"model":    req.Model.ModelID,
		"messages": messages,
	}

	// wantsThinking is what the caller asked for, and it holds on every turn of a
	// tool loop. The forced-choice rule keys on it, not on the per-turn flag, so
	// a forced choice is dropped for the whole loop: sent on a continuation,
	// where thinking is off, it would make the model call a tool again after
	// every result.
	wantsThinking := req.Reasoning.Enabled && req.Model.SupportsReasoning &&
		resolveReasoningStyle(p) == ReasoningAnthropic

	// Once the turn being answered holds a tool call or result, thinking is
	// off. The API wants the signed thinking block that produced a replayed
	// tool_use sent back with it, and the transcript keeps no such block, so a
	// continuation with thinking on is refused. A new question after a finished
	// tool loop is a turn of its own with no tool call to replay, and keeps its
	// reasoning.
	thinking := wantsThinking && !carriesToolTurns(req.Messages)

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
		budget = max(budget, budgetMin)
		if req.MaxTokens <= 0 && req.Model.MaxOutputTokens <= 0 {
			maxTokens = max(maxTokens, budget+budgetHeadroom)
		}
		if clamped, fits := ClampThinkingBudget(budget, maxTokens); fits {
			body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": clamped}
		} else {
			thinking = false
		}
	}
	if !thinking && req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}

	body["max_tokens"] = maxTokens

	if req.System != "" && req.Model.SupportsSystem {
		// Anthropic carries the system prompt beside the messages rather than
		// as one of them.
		body["system"] = req.System
	}

	if len(req.Stop) > 0 {
		body["stop_sequences"] = req.Stop
	}
	if req.CacheControl != nil {
		body["cache_control"] = req.CacheControl
	}
	if len(req.SystemParts) > 0 && req.Model.SupportsSystem {
		parts := make([]map[string]any, 0, len(req.SystemParts))
		for _, part := range req.SystemParts {
			block := map[string]any{"type": "text", "text": part.Text}
			if part.CacheControl != nil {
				block["cache_control"] = part.CacheControl
			}
			parts = append(parts, block)
		}
		body["system"] = parts
	}
	if req.OutputFormat.Type == "json_schema" {
		body["output_config"] = map[string]any{"format": map[string]any{"type": "json_schema", "schema": req.OutputFormat.Schema}}
	}
	if len(req.Tools) > 0 {
		body["tools"] = anthropicTools(req.Tools)
		// While reasoning is requested the API accepts only auto and none, so a
		// forced choice is dropped rather than made into a failed turn — the
		// same trade as the temperature above. It is dropped on continuations
		// too, where thinking itself is off: see wantsThinking.
		if choice, ok := anthropicToolChoice(req.ToolChoice); ok &&
			(!wantsThinking || req.ToolChoice.Mode == ToolChoiceNone || (!thinking && !carriesToolTurns(req.Messages))) {
			body["tool_choice"] = choice
		}
	}

	if req.ParallelToolCalls != nil && len(req.Tools) > 0 {
		choice, ok := body["tool_choice"].(map[string]any)
		if !ok {
			choice = map[string]any{"type": "auto"}
		}
		choice["disable_parallel_tool_use"] = !*req.ParallelToolCalls
		body["tool_choice"] = choice
	}
	for key, value := range req.Extra {
		body[key] = value
	}
	return body, carriedImages
}

// carriesToolTurns reports whether the turn being answered holds a tool call or
// a tool result, which is what makes a request a continuation of a tool loop.
// That turn is everything since the last user message: a loop that finished
// before the user asked something new is history, and its calls need no
// signed block now.
func carriesToolTurns(messages []Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		for _, part := range messages[i].Parts {
			if part.Kind == PartToolCall || part.Kind == PartToolResult {
				return true
			}
		}
		if messages[i].Role == RoleUser {
			return false
		}
	}
	return false
}

func anthropicTools(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		entry := map[string]any{
			"name": tool.Name,
			// The same schema under the name this protocol gives the field.
			"input_schema": schemaOrEmpty(tool.Parameters),
		}
		if tool.CacheControl != nil {
			entry["cache_control"] = tool.CacheControl
		}
		if tool.Strict != nil {
			entry["strict"] = *tool.Strict
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

// ClampThinkingBudget fits a thinking budget under the outgoing token ceiling.
// A zero ceiling means none is set. If even the minimum does not fit, thinking
// is disabled rather than raising a caller's or operator's output limit.
func ClampThinkingBudget(budget, ceiling int) (int, bool) {
	if budget <= 0 {
		// The same default buildBody applies when no budget is named.
		budget = reasoningBudget(EffortMedium)
	}
	if ceiling <= 0 {
		return budget, true
	}
	room := ceiling - budgetHeadroom
	if room < budgetMin {
		return 0, false
	}
	if budget > room {
		budget = room
	}
	return budget, true
}

func reasoningBudget(effort Effort) int {
	switch effort {
	case EffortLow:
		return budgetLow
	case EffortHigh, Effort("xhigh"):
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
					Type: "tool_result", ToolUseID: part.ToolCallID, Content: part.Text, IsError: part.ToolIsError,
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
		if hasCacheControl(message.Parts) {
			// Moving or joining a marked block changes the cached prefix.
			blocks = nil
			for _, part := range message.Parts {
				block := map[string]any{}
				switch part.Kind {
				case PartText:
					block = map[string]any{"type": "text", "text": part.Text}
				case PartImage:
					if !req.Model.SupportsImages || len(part.Data) == 0 {
						continue
					}
					block = map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": part.MediaType, "data": base64.StdEncoding.EncodeToString(part.Data)}}
				case PartToolCall:
					block = map[string]any{"type": "tool_use", "id": part.ToolCallID, "name": part.ToolName, "input": toolInput(part.ToolArgs)}
				case PartToolResult:
					block = map[string]any{"type": "tool_result", "tool_use_id": part.ToolCallID, "content": part.Text, "is_error": part.ToolIsError}
				default:
					continue
				}
				if part.CacheControl != nil {
					block["cache_control"] = part.CacheControl
				}
				blocks = append(blocks, block)
			}
		}
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

	// Trimming the head can strand the results of the calls it held: a user
	// turn that opens with a tool_result whose tool_use is gone is refused.
	// Nothing precedes the head, so every result left there is orphaned, and
	// a turn emptied by that goes too.
	for len(out) > 0 {
		if out[0].Role != "user" {
			out = out[1:]
			continue
		}
		content := out[0].Content.([]any)
		kept := content[:0]
		for _, block := range content {
			_, orphaned := block.(anthropicToolResultBlock)
			if mapped, ok := block.(map[string]any); ok && mapped["type"] == "tool_result" {
				orphaned = true
			}
			if !orphaned {
				kept = append(kept, block)
			}
		}
		if len(kept) == 0 {
			out = out[1:]
			continue
		}
		out[0].Content = kept
		break
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
		return a.readStream(ctx, response, sink, p.APIKey)
	}
	if streaming {
		// Asked for a stream and got a document. Rather than failing, the
		// whole answer is delivered at once and the interface is told why it
		// did not arrive progressively.
		result, err := a.readOnce(response)
		if err != nil {
			return result, err
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
		StopReason   string              `json:"stop_reason"`
		StopSequence *string             `json:"stop_sequence"`
		Usage        anthropicTokenUsage `json:"usage"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return Result{}, unreadable(err, "The provider returned a response we could not read.")
	}
	// A refusal is a delivered answer, so the provider billed the prompt and
	// the refusal output. The counts travel with the error, or the ledger
	// would record the turn as the request fee alone.
	usage := payload.Usage.toUsage()

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
	result.Usage = usage
	result.StopSequence = payload.StopSequence
	if payload.StopReason == "refusal" {
		return result, &Error{Kind: ErrorRefusal, Message: "The model declined to answer."}
	}
	return result, nil
}

type anthropicStreamEvent struct {
	Type string `json:"type"`
	// Which content block this frame belongs to. Tool calls are the reason it
	// matters: a model may open several at once and their argument fragments
	// interleave.
	Index        int `json:"index"`
	ContentBlock struct {
		Type     string          `json:"type"`
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		Input    json.RawMessage `json:"input"`
	} `json:"content_block"`
	Delta struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
		// A tool call's arguments, a few characters at a time.
		PartialJSON  string  `json:"partial_json"`
		StopReason   string  `json:"stop_reason"`
		StopSequence *string `json:"stop_sequence"`
	} `json:"delta"`
	Message struct {
		Usage anthropicTokenUsage `json:"usage"`
	} `json:"message"`
	Usage anthropicTokenUsage `json:"usage"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (anthropicAdapter) readStream(ctx context.Context, response *http.Response, sink Sink, secret string) (Result, error) {
	result := Result{Streamed: true}
	var sinkErr error

	// Accumulated in builders rather than by appending to the strings on
	// result: appending copies the whole answer so far on every delta, so a
	// long generation would spend time and garbage in proportion to its
	// length squared.
	// The strings on result are refreshed as they grow, because a sink error
	// returns result as the partial answer and it has to hold what the reader
	// actually saw.
	var text, reasoning strings.Builder

	// A tool call is its own content block: a start frame naming it, then the
	// arguments as JSON fragments, then a stop. Each call is emitted at its
	// stop — the first moment the arguments are whole, and earlier than the
	// end of the message, so an agent can begin work while the model is
	// still writing the next one.
	//
	// Arguments are collected in a builder beside the call and written onto it
	// at the stop: appending to the string on the call copied everything
	// received so far on every fragment, quadratic in a length the server
	// picks.
	type pendingCall struct {
		call      ToolCall
		arguments strings.Builder
		initial   json.RawMessage
	}
	pending := map[int]*pendingCall{}
	// message_stop is the provider's own word that the answer is complete. A
	// body can end cleanly without it, when something between us and the
	// provider closes the connection after cutting the generation short, and
	// that must not read as a finished answer. The stop_reason that precedes
	// message_stop says the same, and is accepted alone: a compatible provider
	// that sends it and then closes is not cut short.
	sawStop := false
	var tokenUsage anthropicTokenUsage

	err := readEventStream(response.Body, func(data []byte) error {
		if string(data) == "[DONE]" {
			return nil
		}
		var event anthropicStreamEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return malformedStream()
		}

		if event.Type == "" {
			return malformedStream()
		}
		if event.Type == "error" {
			return &Error{Kind: ErrorUpstream, Message: redactSecret(firstNonEmpty(event.Error.Message, "The provider reported an error mid-stream."), secret)}
		}

		// Anthropic reports input tokens when the message starts and output
		// tokens as it ends, so both have to be folded in rather than
		// overwriting one another.
		for _, reported := range [...]anthropicTokenUsage{event.Message.Usage, event.Usage} {
			if !reported.reported() {
				continue
			}
			tokenUsage = tokenUsage.merge(reported)
			result.Usage = result.Usage.Merge(tokenUsage.toUsage())
			if err := sink(Event{Type: EventUsage, Usage: result.Usage}); err != nil {
				sinkErr = err
				return err
			}
		}
		if event.Delta.StopReason != "" {
			result.FinishReason = event.Delta.StopReason
			if event.Type == "message_delta" {
				result.Usage.complete = true
			}
			result.StopSequence = event.Delta.StopSequence
		}

		switch event.Type {
		case "message_stop":
			sawStop = true
			if len(pending) > 0 {
				return incompleteStream()
			}
			return errStreamDone
		case "content_block_start":
			switch event.ContentBlock.Type {
			case "tool_use":
				if _, exists := pending[event.Index]; exists {
					return malformedStream()
				}
				pending[event.Index] = &pendingCall{call: ToolCall{ID: event.ContentBlock.ID, Name: event.ContentBlock.Name}, initial: event.ContentBlock.Input}
			case "text":
				if value := event.ContentBlock.Text; value != "" {
					text.WriteString(value)
					result.Text = text.String()
					if err := sink(Event{Type: EventDelta, Text: value}); err != nil {
						sinkErr = err
						return err
					}
				}
			case "thinking":
				if value := event.ContentBlock.Thinking; value != "" {
					reasoning.WriteString(value)
					result.Reasoning = reasoning.String()
					if err := sink(Event{Type: EventReasoning, Text: value}); err != nil {
						sinkErr = err
						return err
					}
				}
			}
		case "content_block_stop":
			open, found := pending[event.Index]
			if !found {
				return nil
			}
			delete(pending, event.Index)
			call := open.call
			raw := open.arguments.String()
			if raw == "" {
				raw = string(open.initial)
			}
			if !wellFormedArguments(raw) {
				// A stream cut off at max_tokens leaves this half-written. It ends
				// the turn as the OpenAI stream does, rather than running the tool
				// with arguments the model never finished.
				return malformedArguments()
			}
			call.Arguments = toolArguments(raw)
			result.ToolCalls = append(result.ToolCalls, call)
			if err := sink(Event{Type: EventToolCall, ToolCall: call}); err != nil {
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
			text.WriteString(event.Delta.Text)
			result.Text = text.String()
			if err := sink(Event{Type: EventDelta, Text: event.Delta.Text}); err != nil {
				sinkErr = err
				return err
			}
		case "thinking_delta":
			if event.Delta.Thinking == "" {
				return nil
			}
			reasoning.WriteString(event.Delta.Thinking)
			result.Reasoning = reasoning.String()
			if err := sink(Event{Type: EventReasoning, Text: event.Delta.Thinking}); err != nil {
				sinkErr = err
				return err
			}
		case "input_json_delta":
			// A fragment for a call that never started, or one already
			// emitted, has nowhere to go. Both mean a frame arrived out of
			// order, which is not worth ending a good generation over.
			if open, found := pending[event.Index]; found {
				if open.arguments.Len()+len(event.Delta.PartialJSON) > MaxToolArgumentBytes {
					return toolArgumentsTooLarge()
				}
				open.arguments.WriteString(event.Delta.PartialJSON)
			} else {
				return malformedStream()
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
	if len(pending) > 0 || (!sawStop && result.FinishReason == "") {
		// The partial answer is kept, so the caller can still charge for
		// what was generated.
		return result, &Error{Kind: ErrorUpstream, Message: "The answer stopped before it finished."}
	}
	if result.FinishReason == "refusal" {
		return result, &Error{Kind: ErrorRefusal, Message: "The model declined to answer."}
	}
	return result, nil
}

func (anthropicAdapter) GenerateImage(ctx context.Context, client *http.Client, p Provider, req ImageRequest) (ImageResult, error) {
	return ImageResult{}, &Error{Kind: ErrorImagesUnsupported, Message: "Anthropic does not support image generation."}
}
