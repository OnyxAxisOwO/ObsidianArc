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

	for key, value := range req.Extra {
		body[key] = value
	}
	return body, carriedImages
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
		blocks := make([]any, 0, len(message.Parts))
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
				blocks = append(blocks, block)
				carriedImages = true
			}
		}

		// Text first: the models follow an instruction better when it
		// precedes the pictures it is about.
		if text.Len() > 0 {
			blocks = append([]any{anthropicTextBlock{Type: "text", Text: text.String()}}, blocks...)
		}
		if len(blocks) == 0 {
			continue
		}

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
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
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
		}
	}
	result.FinishReason = payload.StopReason
	result.Usage = Usage{InputTokens: payload.Usage.InputTokens, OutputTokens: payload.Usage.OutputTokens}
	return result, nil
}

type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		Thinking   string `json:"thinking"`
		StopReason string `json:"stop_reason"`
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
