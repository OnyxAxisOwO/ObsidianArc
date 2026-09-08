package compat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/reqlog"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

const (
	// The whole request body. Generous for a long transcript with a couple of
	// inline images, short of a request that would be a denial of service by
	// itself.
	maxBodyBytes = 12 << 20
	// Messages in one exchange. A client that has more than this to say is
	// not having a conversation.
	maxMessages = 400
	// Tools in one request. An agent with a few MCP servers attached brings
	// dozens; one that brings hundreds has a configuration problem, not a
	// task.
	maxTools = 256
)

// completionRequest is what an OpenAI client sends.
//
// Only the fields this server can honour are declared. The rest —
// response_format, parallel_tool_calls, logprobs, n, seed and the others —
// are accepted and ignored rather than rejected, because a client library
// that always sends `n: 1` should not be refused for it. Nothing is forwarded
// upstream that is not named here, so an unknown field cannot become a way to
// reach a provider parameter through this endpoint.
type completionRequest struct {
	Model    string        `json:"model"`
	Messages []wireMessage `json:"messages"`
	Stream   *bool         `json:"stream"`

	MaxTokens           *int     `json:"max_tokens"`
	MaxCompletionTokens *int     `json:"max_completion_tokens"`
	Temperature         *float64 `json:"temperature"`
	// OpenAI's own control, translated into the neutral one the adapters
	// take. This is the layer whose job is to speak that dialect.
	ReasoningEffort string `json:"reasoning_effort"`

	Tools []wireTool `json:"tools"`
	// A string or an object, so it is read after the fact rather than typed
	// here.
	ToolChoice json.RawMessage `json:"tool_choice"`
}

// wireTool is one function the caller is offering. The 2023-era `functions`
// field is not read: every client that speaks to an agent today sends
// `tools`, and accepting a second spelling of the same thing is a second
// thing to keep correct.
type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type wireMessage struct {
	Role string `json:"role"`
	// A string, or an array of typed parts. Both are current in the wild, so
	// both are read. Null on an assistant turn that was nothing but tool
	// calls, which is why readContent tolerates one.
	Content json.RawMessage `json:"content"`
	// What the model asked for on an assistant turn the client is replaying.
	ToolCalls []toolCallObject `json:"tool_calls"`
	// On a tool result: which call it answers.
	ToolCallID string `json:"tool_call_id"`
}

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

func (h *Handlers) completions(w http.ResponseWriter, r *http.Request, who caller) error {
	var body completionRequest
	if err := decode(w, r, &body); err != nil {
		return err
	}

	resolved, err := h.authorize(r, who, body.Model)
	if err != nil {
		return err
	}

	request, err := h.buildRequest(body, resolved)
	if err != nil {
		return err
	}

	release, err := h.reserve(r.Context(), who, resolved)
	if err != nil {
		return err
	}
	// Every path out, including the ones that never reach the provider: a
	// reservation that is not given back is an allowance lost until the
	// window rolls over.
	defer release()

	// Echoed back on the answer exactly as it arrived, so a caller who
	// configured "gpt-5.5" reads "gpt-5.5" whatever served it.
	label := strings.TrimSpace(body.Model)

	if request.Stream {
		return h.streamed(w, r, who, request, resolved, label)
	}
	return h.buffered(w, r, who, request, resolved, label)
}

// --- the spine the three surfaces share ---------------------------------------

// authorize maps the name the caller used onto a model they may actually
// send to, and notes it in the request log.
//
// The same authorisation the browser gateway performs, including the one hop
// of routing: a hidden model is refused here exactly as it is there, and a
// routed one answers under the name the caller asked for.
func (h *Handlers) authorize(r *http.Request, who caller, wanted string) (model.Resolved, error) {
	modelID, err := h.resolveModel(r.Context(), who, wanted)
	if err != nil {
		return model.Resolved{}, err
	}

	resolved, err := h.models.Authorize(r.Context(), who.account.GroupID, modelID, who.account.IsAdmin())
	if err != nil {
		return model.Resolved{}, translateModelError(err, wanted)
	}

	reqlog.Annotate(r.Context(), reqlog.Annotation{
		ModelID:   resolved.Model.ID,
		ModelName: resolved.Model.DisplayName,
	})
	return resolved, nil
}

// reserve takes the spend check. Called after the request has been read, so a
// body that was never going to be answered does not hold an allowance while
// it is refused.
func (h *Handlers) reserve(ctx context.Context, who caller, resolved model.Resolved) (func(), error) {
	if h.Guard == nil {
		return func() {}, nil
	}
	free, err := h.Guard(ctx, who.account, resolved.Model)
	if err != nil {
		return nil, translateGuardError(err)
	}
	return free, nil
}

// --- the two shapes -----------------------------------------------------------

func (h *Handlers) buffered(
	w http.ResponseWriter, r *http.Request, who caller,
	request adapter.ChatRequest, resolved model.Resolved, label string,
) error {
	startedAt := time.Now()
	requestID := id.New()

	var reasoning strings.Builder
	var answer strings.Builder
	usage := adapter.Usage{}
	var calls []adapter.ToolCall

	sink := func(event adapter.Event) error {
		switch event.Type {
		case adapter.EventDelta:
			answer.WriteString(event.Text)
		case adapter.EventReasoning:
			reasoning.WriteString(event.Text)
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
		reasoning.Reset()
		reasoning.WriteString(result.Reasoning)
	}
	if len(calls) == 0 {
		calls = result.ToolCalls
	}

	if chatErr != nil {
		h.record(r.Context(), who, resolved, requestID, usage, startedAt, chatErr)
		return translateUpstream(chatErr)
	}
	h.record(r.Context(), who, resolved, requestID, usage, startedAt, nil)

	message := &assistantMessage{
		Role:      "assistant",
		Reasoning: reasoning.String(),
		ToolCalls: renderToolCalls(calls, requestID, 0, false),
	}
	// Null only when the turn was nothing but tool calls; an ordinary answer
	// carries its text, and an empty one carries the empty string.
	if text := answer.String(); text != "" || len(calls) == 0 {
		message.Content = &text
	}

	return writeJSON(w, http.StatusOK, completionResponse{
		ID:      completionID(requestID),
		Object:  "chat.completion",
		Created: startedAt.Unix(),
		Model:   label,
		Choices: []choice{{
			Index:        0,
			Message:      message,
			FinishReason: finishReason(result.FinishReason, len(calls)),
		}},
		Usage: usageOf(usage),
	})
}

// streamed answers as Server-Sent Events in OpenAI's chunk format.
//
// The status is committed the moment the stream opens, so everything that can
// be refused — the key, the model, the allowance — was refused above this,
// while an ordinary JSON error was still possible. A failure from here on can
// only be reported inside the stream.
func (h *Handlers) streamed(
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
	// Whether any answer text has gone out. The opening role chunk does not
	// count: it is the reason this is not simply "have we written anything".
	streamedText := false
	// How many calls have gone out, which is also the index the next one
	// takes: a client assembles a streamed call by its position.
	streamedCalls := 0

	emit := func(delta responseMessage, finish *string) error {
		return sse.Event("", completionChunk{
			ID:      completionID(requestID),
			Object:  "chat.completion.chunk",
			Created: startedAt.Unix(),
			Model:   label,
			Choices: []choice{{Index: 0, Delta: &delta, FinishReason: finish}},
		})
	}

	// The opening chunk carries the role and nothing else, which is what
	// clients expect to see before any content arrives.
	if err := emit(responseMessage{Role: "assistant"}, nil); err != nil {
		return nil
	}

	// Each call goes out in one chunk rather than as argument fragments. The
	// adapters only know a call once it is whole, and a client assembling
	// fragments ends up with exactly this either way.
	emitCalls := func(calls []adapter.ToolCall) error {
		for _, rendered := range renderToolCalls(calls, requestID, streamedCalls, true) {
			streamedCalls++
			if err := emit(responseMessage{ToolCalls: []toolCallObject{rendered}}, nil); err != nil {
				return err
			}
		}
		return nil
	}

	sink := func(event adapter.Event) error {
		switch event.Type {
		case adapter.EventDelta:
			streamedText = true
			return emit(responseMessage{Content: event.Text}, nil)
		case adapter.EventReasoning:
			return emit(responseMessage{Reasoning: event.Text}, nil)
		case adapter.EventToolCall:
			return emitCalls([]adapter.ToolCall{event.ToolCall})
		case adapter.EventUsage:
			usage = usage.Merge(event.Usage)
		}
		return nil
	}

	result, chatErr := h.registry.Chat(r.Context(), resolved.Provider, request, sink)
	usage = usage.Merge(result.Usage)
	// A provider that could not stream answered all at once; the text still
	// has to reach the client as a chunk.
	if result.Text != "" && !streamedText {
		_ = emit(responseMessage{Content: result.Text}, nil)
	}
	// The same for its tool calls — and this is the path a model marked as
	// not streaming always takes, so it is the ordinary case for one of them
	// rather than a fallback.
	if len(result.ToolCalls) > 0 && streamedCalls == 0 {
		_ = emitCalls(result.ToolCalls)
	}

	h.record(context.WithoutCancel(r.Context()), who, resolved, requestID, usage, startedAt, chatErr)

	if chatErr != nil {
		if r.Context().Err() != nil {
			// The client hung up. Writing to a closed connection is not a
			// failure worth reporting to it.
			return nil
		}
		rendered := translateUpstream(chatErr)
		_ = sse.Event("", errorEnvelope{Error: errorBody{
			Message: rendered.message,
			Type:    rendered.kind,
			Code:    rendered.code,
		}})
		_ = sse.Literal("[DONE]")
		return nil
	}

	final := completionChunk{
		ID:      completionID(requestID),
		Object:  "chat.completion.chunk",
		Created: startedAt.Unix(),
		Model:   label,
		Choices: []choice{{
			Index:        0,
			Delta:        &responseMessage{},
			FinishReason: finishReason(result.FinishReason, streamedCalls),
		}},
		Usage: usageOf(usage),
	}
	_ = sse.Event("", final)
	_ = sse.Literal("[DONE]")
	return nil
}

// --- request translation ------------------------------------------------------

func (h *Handlers) buildRequest(body completionRequest, resolved model.Resolved) (adapter.ChatRequest, error) {
	if len(body.Messages) == 0 {
		return adapter.ChatRequest{}, badRequest("messages", "At least one message is required.")
	}
	if len(body.Messages) > maxMessages {
		return adapter.ChatRequest{}, badRequest("messages", "Too many messages in one request.")
	}

	var system strings.Builder
	messages := make([]adapter.Message, 0, len(body.Messages))

	for _, incoming := range body.Messages {
		parts, err := readContent(incoming.Content)
		if err != nil {
			return adapter.ChatRequest{}, err
		}

		switch strings.ToLower(strings.TrimSpace(incoming.Role)) {
		case "system", "developer":
			// Hoisted rather than sent as a turn: the adapters take the
			// system prompt as its own field, and an endpoint without one
			// needs it folded into the first message.
			for _, part := range parts {
				if part.Kind == adapter.PartText {
					if system.Len() > 0 {
						system.WriteString("\n\n")
					}
					system.WriteString(part.Text)
				}
			}
		case "assistant":
			for _, call := range incoming.ToolCalls {
				if call.Function.Name == "" {
					continue
				}
				parts = append(parts, adapter.Part{
					Kind:       adapter.PartToolCall,
					ToolCallID: call.ID,
					ToolName:   call.Function.Name,
					ToolArgs:   call.Function.Arguments,
				})
			}
			if len(parts) > 0 {
				messages = append(messages, adapter.Message{Role: adapter.RoleAssistant, Parts: parts})
			}
		case "user", "":
			if len(parts) > 0 {
				messages = append(messages, adapter.Message{Role: adapter.RoleUser, Parts: parts})
			}
		case "tool", "function":
			// The id is what pairs this with the call it answers, and both
			// protocols refuse a result without one. Saying so is more use
			// than letting the provider say it in its own words.
			if strings.TrimSpace(incoming.ToolCallID) == "" {
				return adapter.ChatRequest{}, badRequest("messages",
					"A tool message must name the call it answers in tool_call_id.")
			}
			// An empty result is a real answer — a tool that returns nothing
			// still returned — so this is not gated on there being text.
			messages = append(messages, adapter.Message{
				Role: adapter.RoleTool,
				Parts: []adapter.Part{{
					Kind:       adapter.PartToolResult,
					ToolCallID: incoming.ToolCallID,
					Text:       joinText(parts),
				}},
			})
		default:
			return adapter.ChatRequest{}, badRequest("messages",
				"Unknown message role: "+incoming.Role+".")
		}
	}

	if len(messages) == 0 {
		return adapter.ChatRequest{}, badRequest("messages",
			"At least one user or assistant message is required.")
	}

	// The operator's instance-wide prompt applies when the caller did not
	// bring one, and yields to theirs when they did: an API client asking for
	// a specific persona is being explicit about it.
	prompt := system.String()
	if prompt == "" {
		prompt = resolved.Model.Prompt(h.settings.Get(settings.DefaultSystemPrompt))
	}

	// Upstream, not Model — this is the request leaving the server, and it is
	// the only place the difference between the two shows.
	maxTokens := ceiling(resolved, body)

	reasoning := adapter.Reasoning{}
	if effort, ok := parseEffort(body.ReasoningEffort); ok &&
		resolved.Model.SupportsReasoning && resolved.Upstream.SupportsReasoning {
		reasoning = adapter.Reasoning{Enabled: true, Effort: effort}
	}

	// False when the field is absent, which is what the protocol says and
	// what every client library assumes. It used to default to true, so a
	// caller that simply did not mention streaming — the official SDK's
	// ordinary call does not — was answered with an event stream where it
	// was parsing a JSON document, and got no further.
	stream := false
	if body.Stream != nil {
		stream = *body.Stream
	}

	tools, err := readTools(body.Tools)
	if err != nil {
		return adapter.ChatRequest{}, err
	}
	choice, err := readToolChoice(body.ToolChoice)
	if err != nil {
		return adapter.ChatRequest{}, err
	}

	return adapter.ChatRequest{
		Model:       resolved.Upstream.Spec(),
		System:      prompt,
		Messages:    messages,
		Temperature: body.Temperature,
		MaxTokens:   maxTokens,
		Reasoning:   reasoning,
		Stream:      stream,
		Tools:       tools,
		ToolChoice:  choice,
	}, nil
}

// readTools converts what the caller offered. The parameter schema is carried
// through untouched: it is the caller's contract with their own function, and
// this layer has no business tidying it.
func readTools(declared []wireTool) ([]adapter.Tool, error) {
	if len(declared) == 0 {
		return nil, nil
	}
	if len(declared) > maxTools {
		return nil, badRequest("tools", "Too many tools in one request.")
	}

	out := make([]adapter.Tool, 0, len(declared))
	for _, tool := range declared {
		// Every tool a chat-completions client can send is a function. A
		// different type names a hosted tool this server has no way to run,
		// and accepting it silently would have the model call something that
		// does not exist.
		if tool.Type != "" && tool.Type != "function" {
			return nil, badRequest("tools", "Unsupported tool type: "+tool.Type+".")
		}
		name := strings.TrimSpace(tool.Function.Name)
		if name == "" {
			return nil, badRequest("tools", "Every tool needs a name.")
		}
		out = append(out, adapter.Tool{
			Name:        name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	return out, nil
}

// readToolChoice reads the field in both shapes it takes: one of the words,
// or an object naming the function that must be called.
func readToolChoice(raw json.RawMessage) (adapter.ToolChoice, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return adapter.ToolChoice{}, nil
	}

	var word string
	if err := json.Unmarshal(raw, &word); err == nil {
		switch strings.ToLower(strings.TrimSpace(word)) {
		case "auto":
			// The default everywhere, so nothing is sent for it.
			return adapter.ToolChoice{}, nil
		case "none":
			return adapter.ToolChoice{Mode: adapter.ToolChoiceNone}, nil
		case "required", "any":
			return adapter.ToolChoice{Mode: adapter.ToolChoiceRequired}, nil
		}
		return adapter.ToolChoice{}, badRequest("tool_choice", "Unknown tool_choice: "+word+".")
	}

	var named struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
		// Where the other protocol's clients put it.
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &named); err != nil {
		return adapter.ToolChoice{}, badRequest("tool_choice",
			"tool_choice must be a string or an object naming a function.")
	}
	name := strings.TrimSpace(named.Function.Name)
	if name == "" {
		name = strings.TrimSpace(named.Name)
	}
	if name == "" {
		return adapter.ToolChoice{}, badRequest("tool_choice",
			"tool_choice must name the function to call.")
	}
	return adapter.ToolChoice{Mode: adapter.ToolChoiceNamed, Name: name}, nil
}

// joinText is a tool result's payload: whatever text the client sent, in the
// order it sent it. A result that arrived as typed parts is flattened,
// because both protocols carry a result as one string.
func joinText(parts []adapter.Part) string {
	var out strings.Builder
	for _, part := range parts {
		if part.Kind == adapter.PartText {
			out.WriteString(part.Text)
		}
	}
	return out.String()
}

// ceiling is how many tokens the answer may run to: what the caller asked
// for, never more than what the model that will answer allows.
func ceiling(resolved model.Resolved, body completionRequest) int {
	limit := resolved.Upstream.MaxOutputTokens
	if limit == 0 {
		limit = resolved.Model.MaxOutputTokens
	}

	asked := 0
	if body.MaxCompletionTokens != nil {
		asked = *body.MaxCompletionTokens
	} else if body.MaxTokens != nil {
		asked = *body.MaxTokens
	}
	if asked <= 0 {
		return limit
	}
	if limit > 0 && asked > limit {
		return limit
	}
	return asked
}

// readContent accepts both shapes the field takes: a plain string, or the
// array of typed parts that multimodal clients send.
func readContent(raw json.RawMessage) ([]adapter.Part, error) {
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

	var parts []contentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, badRequest("messages", "Message content must be a string or an array of parts.")
	}

	out := make([]adapter.Part, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text", "input_text", "":
			if strings.TrimSpace(part.Text) != "" {
				out = append(out, adapter.Part{Kind: adapter.PartText, Text: part.Text})
			}
		case "image_url", "input_image":
			if part.ImageURL == nil {
				continue
			}
			image, err := decodeDataURL(part.ImageURL.URL)
			if err != nil {
				return nil, err
			}
			out = append(out, image)
		default:
			return nil, badRequest("messages", "Unsupported content part: "+part.Type+".")
		}
	}
	return out, nil
}

// decodeDataURL reads an inline image.
//
// Only data: URLs are accepted. A remote one would have this server fetch a
// URL of the caller's choosing, which is a request forgery primitive aimed at
// whatever the server can reach and the internal network it sits in — and the
// browser client has never needed it, because it uploads bytes.
func decodeDataURL(value string) (adapter.Part, error) {
	const refuse = "Images must be inline data: URLs; this server does not fetch remote images."

	if !strings.HasPrefix(value, "data:") {
		return adapter.Part{}, badRequest("messages", refuse)
	}
	meta, payload, found := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !found || !strings.HasSuffix(meta, ";base64") {
		return adapter.Part{}, badRequest("messages", refuse)
	}
	mediaType := strings.TrimSuffix(meta, ";base64")
	if !strings.HasPrefix(mediaType, "image/") {
		return adapter.Part{}, badRequest("messages", "Only images may be sent as content parts.")
	}

	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return adapter.Part{}, badRequest("messages", "Image data is not valid base64.")
	}
	return adapter.Part{Kind: adapter.PartImage, MediaType: mediaType, Data: data}, nil
}

// parseEffort maps OpenAI's control onto the neutral one. "minimal" is folded
// into low rather than refused: it is what recent clients send for "think as
// little as possible", and no adapter here has a fourth level.
func parseEffort(value string) (adapter.Effort, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "minimal", "low":
		return adapter.EffortLow, true
	case "medium":
		return adapter.EffortMedium, true
	case "high":
		return adapter.EffortHigh, true
	}
	return "", false
}

// --- response shapes ----------------------------------------------------------

type completionResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []choice    `json:"choices"`
	Usage   *usageBlock `json:"usage,omitempty"`
}

type completionChunk struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []choice    `json:"choices"`
	Usage   *usageBlock `json:"usage,omitempty"`
}

type choice struct {
	Index   int               `json:"index"`
	Message *assistantMessage `json:"message,omitempty"`
	Delta   *responseMessage  `json:"delta,omitempty"`
	// A pointer so an unfinished chunk renders as null rather than as the
	// empty string. Clients test this field for null to decide whether the
	// answer is complete, and "" is not null.
	FinishReason *string `json:"finish_reason"`
}

// assistantMessage is the answer on a buffered completion.
//
// Separate from responseMessage, whose every field is omitempty because a
// stream chunk carries only what changed. Here `content` is always present,
// null when the turn was nothing but tool calls, because that is what
// OpenAI's schema says and a client decoding into a non-optional field fails
// on a key that is missing rather than null.
type assistantMessage struct {
	Role      string           `json:"role"`
	Content   *string          `json:"content"`
	Reasoning string           `json:"reasoning_content,omitempty"`
	ToolCalls []toolCallObject `json:"tool_calls,omitempty"`
}

type responseMessage struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
	// The model's thinking, under the field name DeepSeek introduced and
	// OpenRouter adopted, since OpenAI's own schema has nowhere to put it.
	//
	// It carries text and only text. Whatever a provider wraps its reasoning
	// in — signatures, block identifiers, encrypted payloads — is consumed by
	// the adapter and never reaches this layer, so there is nothing here to
	// leak even by accident.
	Reasoning string           `json:"reasoning_content,omitempty"`
	ToolCalls []toolCallObject `json:"tool_calls,omitempty"`
}

// toolCallObject is one call, in both directions: it is what this server
// writes on an answer and what a client replays on the assistant turn after
// it.
type toolCallObject struct {
	// Set only inside a stream chunk, where it is how a client knows which
	// call a fragment belongs to. A finished message has no use for it and
	// OpenAI does not send one. A pointer because the first call's index is
	// 0, which omitempty would drop.
	Index    *int             `json:"index,omitempty"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function toolCallFunction `json:"function"`
}

type toolCallFunction struct {
	Name string `json:"name"`
	// JSON text, which is what the protocol carries — not an object.
	Arguments string `json:"arguments"`
}

// renderToolCalls writes the calls of one answer. `indexed` numbers them, for
// a stream chunk.
func renderToolCalls(calls []adapter.ToolCall, requestID string, from int, indexed bool) []toolCallObject {
	out := make([]toolCallObject, 0, len(calls))
	for offset, call := range calls {
		position := from + offset
		rendered := toolCallObject{
			ID:   callID(requestID, position),
			Type: "function",
			Function: toolCallFunction{
				Name:      call.Name,
				Arguments: call.Arguments,
			},
		}
		if indexed {
			rendered.Index = &position
		}
		out = append(out, rendered)
	}
	return out
}

// callID is the identifier a tool call is answered by.
//
// Minted here rather than passed through, for the reason this whole package
// exists: a provider's own id is a provider's own spelling, and a `toolu_`
// prefix names the family that answered as plainly as a header would. The
// client echoes this back on both the call and its result, and both protocols
// only ever check that the two match inside the transcript they were sent —
// so a name of our own does the same work and says nothing.
func callID(requestID string, position int) string {
	return "call_" + requestID + "_" + strconv.Itoa(position)
}

type usageBlock struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	Details          *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details,omitempty"`
}

func usageOf(u adapter.Usage) *usageBlock {
	if u.Total() == 0 {
		return nil
	}
	block := &usageBlock{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.Total(),
	}
	if u.ReasoningTokens > 0 {
		block.Details = &struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		}{ReasoningTokens: u.ReasoningTokens}
	}
	return block
}

// finishReason maps whatever the provider called it onto OpenAI's vocabulary.
//
// Not passed through: Anthropic says "end_turn" and "max_tokens", and a
// caller who saw those would learn which family answered a model the operator
// presented under their own name.
//
// A turn that produced calls ends in "tool_calls" whatever the provider called
// it, because that is the field an agent's loop branches on — one that reads
// "stop" stops, with the calls it was about to run still in its hand. Not
// over "length" or "content_filter" though: those say the turn was cut short,
// which the client needs to hear more than it needs to be sent round again.
func finishReason(upstream string, toolCalls int) *string {
	reason := "stop"
	switch strings.ToLower(strings.TrimSpace(upstream)) {
	case "length", "max_tokens", "model_length":
		reason = "length"
	case "content_filter", "refusal":
		reason = "content_filter"
	case "tool_calls", "tool_use", "function_call":
		reason = "tool_calls"
	}
	if toolCalls > 0 && reason == "stop" {
		reason = "tool_calls"
	}
	return &reason
}

func completionID(requestID string) string { return "chatcmpl-" + requestID }

// --- accounting ---------------------------------------------------------------

// record writes the turn to the usage ledger and settles the allowance,
// through the same hook the browser gateway uses. An API turn differs from a
// browser one only in having no conversation and no message to point at.
func (h *Handlers) record(
	ctx context.Context, who caller, resolved model.Resolved,
	requestID string, usage adapter.Usage, startedAt time.Time, failure error,
) {
	if h.OnTurn == nil {
		return
	}

	status := chat.StatusOK
	code := ""
	if failure != nil {
		status = chat.StatusError
		if errors.Is(failure, context.Canceled) {
			status = chat.StatusAborted
			code = "cancelled"
		} else {
			code, _ = chat.Describe(failure)
		}
	}

	// Detached and bounded: the request context is cancelled the instant the
	// client hangs up, and a turn that is not recorded is one the account
	// never pays for.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()

	h.OnTurn(recordCtx, chat.TurnRecord{
		User:         who.account,
		Model:        resolved.Model,
		ProviderID:   resolved.Provider.ID,
		ProviderName: resolved.Provider.Name,
		RequestID:    requestID,
		Usage:        usage,
		Credits:      resolved.Model.Credits(usage),
		Status:       status,
		ErrorCode:    code,
		StartedAt:    startedAt,
		FinishedAt:   time.Now(),
	})
}

// --- error translation --------------------------------------------------------

// decode reads a /v1 body.
//
// Lenient, unlike the rest of this server: an unknown field on an internal
// endpoint is a typo worth reporting, but this endpoint implements somebody
// else's protocol, and that protocol grows fields on its own schedule. The
// case that matters is a client replaying an assistant turn it was given —
// every SDK sends the whole message back, `refusal` and `annotations` and
// whatever was added last month included, and refusing one of those ends the
// agent's loop on its second step. Which fields are honoured is decided by
// completionRequest, so nothing reaches a provider by being named here.
func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	if err := httpx.DecodeJSONLenient(w, r, dst, maxBodyBytes); err != nil {
		var decided *httpx.Error
		if errors.As(err, &decided) {
			return apiError{
				status:  decided.Status,
				kind:    "invalid_request_error",
				code:    "invalid_body",
				message: decided.Message,
			}
		}
		return internalError(err)
	}
	return nil
}

// translateModelError keeps "no such model" and "not allowed to use it"
// indistinguishable, which is what stops the endpoint being used to find out
// what the operator has configured.
func translateModelError(err error, requested string) error {
	switch {
	case errors.Is(err, model.ErrNotPermitted), errors.Is(err, model.ErrNotFound):
		return unknownModel(requested)
	case errors.Is(err, model.ErrDisabled):
		return apiError{
			status:  http.StatusServiceUnavailable,
			kind:    "server_error",
			code:    "model_unavailable",
			message: "That model is currently unavailable.",
		}
	}
	return internalError(err)
}

// translateGuardError renders a refusal from the shared spend check. The
// httpx error it produces already carries a decided status and a sentence
// written for a person; only the envelope changes.
func translateGuardError(err error) error {
	var decided *httpx.Error
	if !errors.As(err, &decided) {
		return internalError(err)
	}
	kind := "invalid_request_error"
	if decided.Status == http.StatusTooManyRequests {
		kind = "rate_limit_error"
	}
	return apiError{
		status:  decided.Status,
		kind:    kind,
		code:    decided.Code,
		message: decided.Message,
	}
}

// translateUpstream renders a provider failure. The sentence comes from the
// gateway's own classifier, so an authentication failure against a provider
// says the same careful nothing here that it says in the browser.
func translateUpstream(err error) apiError {
	if errors.Is(err, context.Canceled) {
		return apiError{
			status:  499,
			kind:    "invalid_request_error",
			code:    "cancelled",
			message: "The request was cancelled.",
		}
	}

	code, message := chat.Describe(err)
	status := http.StatusBadGateway
	kind := "server_error"
	switch code {
	case "provider_rate_limited":
		status, kind = http.StatusTooManyRequests, "rate_limit_error"
	case "refusal", "images_unsupported":
		status, kind = http.StatusBadRequest, "invalid_request_error"
	case "internal":
		status = http.StatusInternalServerError
	}

	slog.Error("api completion failed", "code", code, "error", err)
	return apiError{status: status, kind: kind, code: code, message: message}
}
