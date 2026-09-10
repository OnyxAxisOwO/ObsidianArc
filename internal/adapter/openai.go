package adapter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
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
	Role string `json:"role"`
	// Left null on an assistant turn that was nothing but tool calls, which
	// is what the protocol says and what the models emit.
	Content any `json:"content"`
	// Omitted rather than sent empty: several compatible servers reject a
	// null or `[]` here on an ordinary message.
	ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
	// On a tool result, naming the call it answers.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name string `json:"name"`
	// JSON text rather than an object, which is what the protocol carries in
	// both directions.
	Arguments string `json:"arguments"`
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

	if len(req.Tools) > 0 {
		body["tools"] = openAITools(req.Tools)
		if choice, ok := openAIToolChoice(req.ToolChoice); ok {
			body["tool_choice"] = choice
		}
	}

	for key, value := range req.Extra {
		body[key] = value
	}
	return body, carriedImages
}

func openAITools(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		function := map[string]any{"name": tool.Name, "parameters": schemaOrEmpty(tool.Parameters)}
		if tool.Description != "" {
			function["description"] = tool.Description
		}
		out = append(out, map[string]any{"type": "function", "function": function})
	}
	return out
}

func openAIToolChoice(choice ToolChoice) (any, bool) {
	switch choice.Mode {
	case ToolChoiceNone:
		return "none", true
	case ToolChoiceRequired:
		return "required", true
	case ToolChoiceNamed:
		if choice.Name == "" {
			return nil, false
		}
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": choice.Name},
		}, true
	}
	// Auto is the default on every endpoint, so saying it adds a field that
	// an older compatible server might not know and changes nothing.
	return nil, false
}

func (openAIAdapter) buildMessages(req ChatRequest) ([]openAIMessage, bool) {
	out := make([]openAIMessage, 0, len(req.Messages))
	carriedImages := false

	for _, message := range req.Messages {
		// A tool result is a message of its own here, one per call answered.
		// It never merges with anything: the protocol pairs a result with a
		// call by id, and two of them in one message would leave one call
		// unanswered.
		if message.Role == RoleTool {
			for _, part := range message.Parts {
				if part.Kind != PartToolResult || part.ToolCallID == "" {
					continue
				}
				out = append(out, openAIMessage{
					Role: "tool", Content: part.Text, ToolCallID: part.ToolCallID,
				})
			}
			continue
		}

		role := "user"
		if message.Role == RoleAssistant {
			role = "assistant"
		}

		parts := make([]any, 0, len(message.Parts))
		var calls []openAIToolCall
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
			case PartToolCall:
				if part.ToolName == "" {
					continue
				}
				calls = append(calls, openAIToolCall{
					ID:   part.ToolCallID,
					Type: "function",
					Function: openAIToolFunction{
						Name:      part.ToolName,
						Arguments: toolArguments(part.ToolArgs),
					},
				})
			}
		}

		var content any
		if len(parts) == 0 {
			// A plain string, not a one-element array: several compatible
			// servers only accept the simple form, and the vast majority of
			// messages have no attachment.
			if text.Len() > 0 {
				content = text.String()
			} else if len(calls) == 0 {
				continue
			}
			// An assistant turn that was nothing but tool calls still
			// travels, with a null content. Dropping it would leave the
			// results after it answering a call the transcript no longer
			// contains, which both protocols refuse.
		} else {
			if text.Len() > 0 {
				parts = append([]any{openAITextPart{Type: "text", Text: text.String()}}, parts...)
			}
			content = parts
		}

		// Merging consecutive same-role messages repairs an edited
		// transcript. A message carrying tool calls is not one to repair:
		// the calls after it are addressed by id, and folding two turns
		// together would put a call and its answer on the same side.
		if last := len(out) - 1; last >= 0 && out[last].Role == role && content != nil &&
			len(calls) == 0 && len(out[last].ToolCalls) == 0 {
			out[last].Content = mergeOpenAIContent(out[last].Content, content)
			continue
		}
		out = append(out, openAIMessage{Role: role, Content: content, ToolCalls: calls})
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
				Reasoning        string           `json:"reasoning"`
				ReasoningContent string           `json:"reasoning_content"`
				Refusal          string           `json:"refusal"`
				ToolCalls        []openAIToolCall `json:"tool_calls"`
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
	for _, call := range choice.Message.ToolCalls {
		if call.Function.Name == "" {
			continue
		}
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: toolArguments(call.Function.Arguments),
		})
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
			Content          string                `json:"content"`
			Reasoning        string                `json:"reasoning"`
			ReasoningContent string                `json:"reasoning_content"`
			ToolCalls        []openAIToolCallDelta `json:"tool_calls"`
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

// One frame's worth of a tool call. Everything but the position is optional:
// the first frame usually carries the id and the name, and the ones after it
// carry a few more characters of the arguments and nothing else.
type openAIToolCallDelta struct {
	// A pointer because a server with one call in flight may leave it out,
	// and the zero value is a valid position.
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// toolCallAssembly reassembles the calls of one streamed answer.
//
// The wire index is the identity, not the order of arrival: a server may
// interleave two calls' argument fragments, and OpenAI's own client keys on
// the index for exactly that reason.
type toolCallAssembly struct {
	calls []ToolCall
	at    map[int]int
}

func (a *toolCallAssembly) absorb(fragments []openAIToolCallDelta) {
	for _, fragment := range fragments {
		index := 0
		if fragment.Index != nil {
			index = *fragment.Index
		}
		if a.at == nil {
			a.at = map[int]int{}
		}
		position, seen := a.at[index]
		if !seen {
			position = len(a.calls)
			a.at[index] = position
			a.calls = append(a.calls, ToolCall{})
		}
		if fragment.ID != "" {
			a.calls[position].ID = fragment.ID
		}
		// Assigned rather than appended: a server that repeats the whole
		// name on every fragment is more common than one that splits it, and
		// appending would turn "search" into "searchsearchsearch".
		if fragment.Function.Name != "" {
			a.calls[position].Name = fragment.Function.Name
		}
		a.calls[position].Arguments += fragment.Function.Arguments
	}
}

// finish emits each assembled call, once, now that no more fragments are
// coming.
func (a *toolCallAssembly) finish(result *Result, sink Sink) error {
	for _, call := range a.calls {
		if call.Name == "" {
			// Fragments for a call whose name never arrived. Nothing can be
			// invoked from that, and forwarding it would have the client
			// call a tool it does not have.
			continue
		}
		call.Arguments = toolArguments(call.Arguments)
		result.ToolCalls = append(result.ToolCalls, call)
		if err := sink(Event{Type: EventToolCall, ToolCall: call}); err != nil {
			return err
		}
	}
	return nil
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
		// Kept across deltas rather than rebuilt per delta: see inlineThinking.
		thinking inlineThinking
		tools    toolCallAssembly
	)

	flushContent := func(final bool) error {
		inline, answer := thinking.split(content.String())

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

		// Before the early return below: a tool-call frame usually carries no
		// content at all, so reading it after that test would read none of
		// them.
		tools.absorb(choice.Delta.ToolCalls)

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
	if err := tools.finish(&result, sink); err != nil {
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

func (openAIAdapter) GenerateImage(ctx context.Context, client *http.Client, p Provider, req ImageRequest) (ImageResult, error) {
	endpoint := imagesEndpoint(p.BaseURL)

	var (
		response *http.Response
		err      error
	)

	if len(req.Image) > 0 {
		endpoint = imageEditsEndpoint(p.BaseURL)
		contentType, encoded, buildErr := imageEditForm(req)
		if buildErr != nil {
			return ImageResult{}, buildErr
		}
		response, err = post(ctx, client, p, endpoint, contentType, encoded)
	} else {
		body := map[string]any{
			"model":  req.Model,
			"prompt": req.Prompt,
		}
		if req.Size != "" {
			body["size"] = req.Size
		}
		if req.Style != "" {
			body["style"] = req.Style
		}
		if req.Quality != "" {
			body["quality"] = req.Quality
		}
		if req.N > 0 {
			body["n"] = req.N
		}
		if req.ResponseFormat != "" {
			body["response_format"] = req.ResponseFormat
		} else {
			body["response_format"] = "b64_json"
		}
		response, err = postJSON(ctx, client, p, endpoint, body)
	}
	if err != nil {
		return ImageResult{}, err
	}
	defer response.Body.Close()

	if response.StatusCode >= 400 {
		return ImageResult{}, classifyHTTP(p, endpoint, response.StatusCode,
			response.Header.Get("Retry-After"), readErrorBody(response), false)
	}

	var payload struct {
		Created int64 `json:"created"`
		Data    []struct {
			URL           string `json:"url"`
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return ImageResult{}, &Error{Kind: ErrorUpstream, Message: "The provider returned an image response we could not read.", cause: err}
	}
	if len(payload.Data) == 0 {
		return ImageResult{}, &Error{Kind: ErrorUpstream, Message: "The provider returned no images."}
	}

	out := ImageResult{
		Created: payload.Created,
		Data:    make([]GeneratedImage, 0, len(payload.Data)),
	}
	for _, item := range payload.Data {
		out.Data = append(out.Data, GeneratedImage{
			URL:           item.URL,
			B64JSON:       item.B64JSON,
			RevisedPrompt: item.RevisedPrompt,
		})
	}
	return out, nil
}

// imageEditForm encodes an edit request the way that endpoint takes it:
// multipart, with the picture as a file part.
//
// Deliberately without `response_format`: the current image models reject the
// parameter on this endpoint and answer with bytes anyway, and the older ones
// that do accept it default to a link — which the caller takes delivery of in
// either case. Sending it would fail the request that needs it least.
func imageEditForm(req ImageRequest) (string, []byte, error) {
	var buffer bytes.Buffer
	form := multipart.NewWriter(&buffer)

	fields := [][2]string{
		{"model", req.Model},
		{"prompt", req.Prompt},
		{"size", req.Size},
		{"quality", req.Quality},
	}
	if req.N > 0 {
		fields = append(fields, [2]string{"n", strconv.Itoa(req.N)})
	}
	for _, field := range fields {
		if field[1] == "" {
			continue
		}
		if err := form.WriteField(field[0], field[1]); err != nil {
			return "", nil, &Error{Kind: ErrorInvalidRequest, Message: "Could not encode the request.", cause: err}
		}
	}

	mime := req.ImageMime
	if mime == "" {
		mime = "image/png"
	}
	headers := make(textproto.MIMEHeader)
	headers.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="image"; filename="image%s"`, imageExtension(mime)))
	// The part's own type, rather than the octet-stream a plain file part
	// would carry: providers that sniff the upload by media type refuse the
	// generic one outright.
	headers.Set("Content-Type", mime)
	part, err := form.CreatePart(headers)
	if err != nil {
		return "", nil, &Error{Kind: ErrorInvalidRequest, Message: "Could not encode the request.", cause: err}
	}
	if _, err := part.Write(req.Image); err != nil {
		return "", nil, &Error{Kind: ErrorInvalidRequest, Message: "Could not encode the request.", cause: err}
	}
	if err := form.Close(); err != nil {
		return "", nil, &Error{Kind: ErrorInvalidRequest, Message: "Could not encode the request.", cause: err}
	}
	return form.FormDataContentType(), buffer.Bytes(), nil
}

// The filename is not decoration: some providers read the extension rather
// than the part's media type to decide what was uploaded.
func imageExtension(mime string) string {
	switch mime {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
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

// toolArguments is the arguments field as the protocols want it: JSON text,
// always parseable. A call with no arguments is a call with `{}` — an empty
// string is not valid JSON, and a client that decodes it fails on a tool the
// model invoked correctly.
func toolArguments(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "{}"
	}
	return raw
}

// schemaOrEmpty is the parameter schema a tool travels with. A tool that
// declared none takes no arguments, which is a schema both protocols accept
// — where a missing `parameters` is refused outright by several servers.
func schemaOrEmpty(schema json.RawMessage) any {
	if len(bytes.TrimSpace(schema)) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return schema
}

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
	for _, call := range result.ToolCalls {
		if err := sink(Event{Type: EventToolCall, ToolCall: call}); err != nil {
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
