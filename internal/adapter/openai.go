package adapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// The OpenAI chat-completions shape, which is what almost every
// non-Anthropic endpoint speaks: OpenAI itself, DeepSeek, xAI, OpenRouter,
// Groq, Together, a local Ollama or vLLM.
//
// "Compatible" is generous in practice — servers differ over where reasoning
// appears, whether usage is reported at all, and which flag turns thinking
// on. Those differences are handled here, not by asking an operator to pick a
// vendor from a list.
type openAIAdapter struct{}

func (openAIAdapter) Kind() Kind { return KindOpenAI }

type openAIMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type openAITextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openAIImagePart struct {
	Type     string `json:"type"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

func (a openAIAdapter) buildBody(p Provider, req ChatRequest) (map[string]any, bool) {
	messages, carriedImages := a.buildMessages(req)

	if req.System != "" && req.Model.SupportsSystem {
		// Unlike Anthropic, the system prompt is a message here.
		messages = append([]openAIMessage{{Role: "system", Content: req.System}}, messages...)
	}

	body := map[string]any{
		"model":    req.Model.ModelID,
		"messages": messages,
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = req.Model.MaxOutputTokens
	}
	if maxTokens > 0 {
		// max_tokens rather than max_completion_tokens: the newer field is
		// not understood by most compatible servers, and the older one is
		// still accepted by the ones that prefer it.
		body["max_tokens"] = maxTokens
	}

	if req.Reasoning.Enabled && req.Model.SupportsReasoning {
		effort := string(req.Reasoning.Effort)
		if effort == "" {
			effort = string(EffortMedium)
		}
		switch resolveReasoningStyle(p) {
		case ReasoningEffort:
			body["reasoning_effort"] = effort
		case ReasoningOpenRouter:
			body["reasoning"] = map[string]any{"effort": effort}
		case ReasoningQwen:
			body["enable_thinking"] = true
		case ReasoningNone:
			// The endpoint has no switch. Reasoning models on such servers
			// emit <think>…</think> inline, which the reader below splits out
			// anyway, so the feature still works.
		}
	}

	for key, value := range req.Extra {
		body[key] = value
	}
	return body, carriedImages
}

func (openAIAdapter) buildMessages(req ChatRequest) ([]openAIMessage, bool) {
	out := make([]openAIMessage, 0, len(req.Messages))
	carriedImages := false

	for _, message := range req.Messages {
		role := "user"
		if message.Role == RoleAssistant {
			role = "assistant"
		}

		parts := make([]any, 0, len(message.Parts))
		text := strings.Builder{}
		for _, part := range message.Parts {
			switch part.Kind {
			case PartText:
				text.WriteString(part.Text)
			case PartImage:
				if !req.Model.SupportsImages || len(part.Data) == 0 {
					continue
				}
				block := openAIImagePart{Type: "image_url"}
				block.ImageURL.URL = "data:" + part.MediaType + ";base64," +
					base64.StdEncoding.EncodeToString(part.Data)
				parts = append(parts, block)
				carriedImages = true
			}
		}

		var content any
		if len(parts) == 0 {
			// A plain string, not a one-element array: several compatible
			// servers only accept the simple form, and the vast majority of
			// messages have no attachment.
			if text.Len() == 0 {
				continue
			}
			content = text.String()
		} else {
			if text.Len() > 0 {
				parts = append([]any{openAITextPart{Type: "text", Text: text.String()}}, parts...)
			}
			content = parts
		}

		if last := len(out) - 1; last >= 0 && out[last].Role == role {
			out[last].Content = mergeOpenAIContent(out[last].Content, content)
			continue
		}
		out = append(out, openAIMessage{Role: role, Content: content})
	}

	for len(out) > 0 && out[0].Role != "user" {
		out = out[1:]
	}
	return out, carriedImages
}

// Two consecutive same-role messages are malformed for both protocols, and an
// edited transcript can produce them.
func mergeOpenAIContent(existing, incoming any) any {
	left, leftIsText := existing.(string)
	right, rightIsText := incoming.(string)
	if leftIsText && rightIsText {
		return left + "\n\n" + right
	}

	toParts := func(value any) []any {
		if text, ok := value.(string); ok {
			return []any{openAITextPart{Type: "text", Text: text}}
		}
		if parts, ok := value.([]any); ok {
			return parts
		}
		return nil
	}
	return append(toParts(existing), toParts(incoming)...)
}

func (a openAIAdapter) Chat(ctx context.Context, client *http.Client, p Provider, req ChatRequest, sink Sink) (Result, error) {
	body, carriedImages := a.buildBody(p, req)
	endpoint := chatEndpoint(KindOpenAI, p.BaseURL)

	streaming := req.Stream && req.Model.SupportsStreaming
	if streaming {
		body["stream"] = true
		// Without this most servers omit usage entirely on a streamed
		// response, which would leave every streamed turn unbilled.
		body["stream_options"] = map[string]any{"include_usage": true}
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

func (openAIAdapter) readOnce(response *http.Response) (Result, error) {
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				// Two spellings are in the wild for the same thing.
				Reasoning        string `json:"reasoning"`
				ReasoningContent string `json:"reasoning_content"`
				Refusal          string `json:"refusal"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage openAIUsage `json:"usage"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return Result{}, &Error{Kind: ErrorUpstream, Message: "The provider returned a response we could not read.", cause: err}
	}
	if len(payload.Choices) == 0 {
		return Result{}, &Error{Kind: ErrorUpstream, Message: "The provider returned no answer."}
	}

	choice := payload.Choices[0]
	if choice.Message.Refusal != "" {
		return Result{}, &Error{Kind: ErrorRefusal, Message: choice.Message.Refusal}
	}

	reasoning, answer := splitThinking(choice.Message.Content)
	result := Result{
		Text:         answer,
		Reasoning:    firstNonEmpty(choice.Message.ReasoningContent, choice.Message.Reasoning) + reasoning,
		FinishReason: choice.FinishReason,
		Usage:        payload.Usage.toUsage(),
	}
	return result, nil
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	// Where the reasoning cost is reported, when it is reported separately.
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u openAIUsage) toUsage() Usage {
	return Usage{
		InputTokens:     u.PromptTokens,
		OutputTokens:    u.CompletionTokens,
		ReasoningTokens: u.CompletionTokensDetails.ReasoningTokens,
	}
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage"`
	// Groq reports usage in a namespace of its own.
	XGroq struct {
		Usage *openAIUsage `json:"usage"`
	} `json:"x_groq"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (openAIAdapter) readStream(ctx context.Context, response *http.Response, sink Sink) (Result, error) {
	result := Result{Streamed: true}
	var sinkErr error

	// Reasoning reaches us two ways, and a stream can use both.
	//
	// A dedicated field (`reasoning` / `reasoning_content`) arrives already
	// separated and is passed straight through. Inline `<think>…</think>`,
	// which is what several reasoning models emit on servers with no
	// reasoning field, has to be re-derived from the whole content buffer on
	// every delta, because the tag can straddle two of them. The counters
	// below are how each event ends up carrying only what is new.
	var (
		content        strings.Builder
		fieldReasoning strings.Builder
		emittedAnswer  int
		emittedInline  int
	)

	flushContent := func(final bool) error {
		inline, answer := splitThinking(content.String())

		// A trailing `<th` is not yet text — it may be the start of a tag.
		// Holding it back until the next delta is what stops a stray `<th`
		// appearing in the answer and then vanishing.
		visible := len(answer)
		if !final {
			visible -= partialThinkTagLen(answer)
		}

		if len(inline) > emittedInline {
			delta := inline[emittedInline:]
			emittedInline = len(inline)
			if err := sink(Event{Type: EventReasoning, Text: delta}); err != nil {
				return err
			}
		}
		if visible > emittedAnswer {
			delta := answer[emittedAnswer:visible]
			emittedAnswer = visible
			if err := sink(Event{Type: EventDelta, Text: delta}); err != nil {
				return err
			}
		}
		result.Text = answer
		result.Reasoning = fieldReasoning.String() + inline
		return nil
	}

	err := readEventStream(response.Body, func(data []byte) error {
		var chunk openAIStreamChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return nil
		}
		if chunk.Error.Message != "" {
			return &Error{Kind: ErrorUpstream, Message: chunk.Error.Message}
		}

		for _, usage := range []*openAIUsage{chunk.Usage, chunk.XGroq.Usage} {
			if usage == nil {
				continue
			}
			result.Usage = result.Usage.Merge(usage.toUsage())
			if err := sink(Event{Type: EventUsage, Usage: result.Usage}); err != nil {
				sinkErr = err
				return err
			}
		}
		if len(chunk.Choices) == 0 {
			return nil
		}

		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			result.FinishReason = choice.FinishReason
		}

		if thought := firstNonEmpty(choice.Delta.ReasoningContent, choice.Delta.Reasoning); thought != "" {
			fieldReasoning.WriteString(thought)
			result.Reasoning = fieldReasoning.String()
			if err := sink(Event{Type: EventReasoning, Text: thought}); err != nil {
				sinkErr = err
				return err
			}
		}

		if choice.Delta.Content == "" {
			return nil
		}
		content.WriteString(choice.Delta.Content)
		if err := flushContent(false); err != nil {
			sinkErr = err
			return err
		}
		return nil
	})

	if err != nil {
		if sinkErr != nil {
			return result, sinkErr
		}
		var adapterErr *Error
		if asAdapterError(err, &adapterErr) {
			return result, adapterErr
		}
		return result, networkError(ctx, err)
	}

	// Release anything held back as a possible tag prefix now that no more
	// deltas are coming.
	if err := flushContent(true); err != nil {
		return result, err
	}
	return result, nil
}

// partialThinkTagLen reports how many trailing characters could still grow
// into a `<think>` opening tag.
func partialThinkTagLen(text string) int {
	for length := len(thinkOpen) - 1; length > 0; length-- {
		if strings.HasSuffix(text, thinkOpen[:length]) {
			return length
		}
	}
	return 0
}

// --- listing -----------------------------------------------------------------

func (openAIAdapter) ListModels(ctx context.Context, client *http.Client, p Provider) ([]RemoteModel, error) {
	return listModels(ctx, client, p)
}

func (anthropicAdapter) ListModels(ctx context.Context, client *http.Client, p Provider) ([]RemoteModel, error) {
	return listModels(ctx, client, p)
}

// Both protocols answer a models listing with `{"data": [...]}`, differing
// only in whether entries carry a display name, so one implementation serves
// both.
func listModels(ctx context.Context, client *http.Client, p Provider) ([]RemoteModel, error) {
	endpoint := modelsEndpoint(p.Kind, p.BaseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidRequest, Message: "Invalid provider endpoint.", cause: err}
	}
	applyHeaders(req, p)

	response, err := client.Do(req)
	if err != nil {
		return nil, networkError(ctx, err)
	}
	defer response.Body.Close()

	if response.StatusCode >= 400 {
		return nil, classifyHTTP(p, endpoint, response.StatusCode,
			response.Header.Get("Retry-After"), readErrorBody(response), false)
	}

	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
		Models []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, &Error{Kind: ErrorUpstream, Message: "The endpoint answered with something other than a model list.", cause: err}
	}

	entries := payload.Data
	if len(entries) == 0 {
		entries = payload.Models
	}

	out := make([]RemoteModel, 0, len(entries))
	for _, entry := range entries {
		id := firstNonEmpty(entry.ID, entry.Name)
		if id == "" {
			continue
		}
		display := entry.DisplayName
		if display == id {
			display = ""
		}
		out = append(out, RemoteModel{ID: id, DisplayName: display})
	}
	return out, nil
}

// --- shared helpers ----------------------------------------------------------

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func fallbackContentType(value string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return "no content type"
}

// emitAll replays a non-streamed answer through the sink, so the caller's
// streaming path is the only path and a fallback needs no separate handling
// upstream.
func emitAll(sink Sink, result Result) error {
	if result.Reasoning != "" {
		if err := sink(Event{Type: EventReasoning, Text: result.Reasoning}); err != nil {
			return err
		}
	}
	if result.Text != "" {
		if err := sink(Event{Type: EventDelta, Text: result.Text}); err != nil {
			return err
		}
	}
	if result.Usage.Total() > 0 {
		if err := sink(Event{Type: EventUsage, Usage: result.Usage}); err != nil {
			return err
		}
	}
	return nil
}

func asAdapterError(err error, target **Error) bool {
	return errors.As(err, target)
}
