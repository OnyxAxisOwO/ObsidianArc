// Package adapter is the only place in this server that knows what an AI
// provider's wire format looks like.
//
// Everything above it — the chat gateway, the usage ledger, the handlers —
// speaks one request shape and consumes one event stream. There is no
// `if kind == "openai"` outside this package, and adding a third protocol is
// a new file here plus a row in the registry, not a change anywhere else.
//
// The shapes below are deliberately smaller than what either provider can
// express: text, images and tool calls in, text, reasoning and tool calls
// out. Structured output and the rest are absent because nothing above this
// layer has anywhere to put them yet, and a field with no consumer is a field
// that drifts.
//
// Tool calls are here because the /v1 API has a consumer for them — an agent
// on the other end that cannot work without them — and because they are the
// one thing a caller cannot fake from outside: everything else an OpenAI
// client sends can be folded into the prompt, but a tool call has to come
// back from the provider as its own thing to be one.
package adapter

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

type Kind string

const (
	KindOpenAI    Kind = "openai"
	KindAnthropic Kind = "anthropic"
)

func (k Kind) Valid() bool { return k == KindOpenAI || k == KindAnthropic }

// ReasoningStyle names how a provider wants to be told to think before
// answering. It is a per-provider setting rather than a branch in code
// because "which flag does this endpoint want" is exactly the kind of detail
// that differs between two servers speaking the same protocol — and an
// operator can fix a new one by choosing a value, not by waiting for a
// release.
type ReasoningStyle string

const (
	// Whatever is standard for the protocol: thinking blocks for Anthropic,
	// reasoning_effort for OpenAI-compatible.
	ReasoningAuto ReasoningStyle = "auto"
	// The endpoint has no switch; the model reasons or it does not.
	ReasoningNone ReasoningStyle = "none"
	// Anthropic: thinking: {type: "enabled", budget_tokens: N}
	ReasoningAnthropic ReasoningStyle = "anthropic"
	// OpenAI and most compatible servers: reasoning_effort: "low"|"medium"|"high"
	ReasoningEffort ReasoningStyle = "openai_effort"
	// OpenRouter: reasoning: {effort: "..."}
	ReasoningOpenRouter ReasoningStyle = "openrouter"
	// Qwen and several Chinese endpoints: enable_thinking: true
	ReasoningQwen ReasoningStyle = "qwen"
)

func (s ReasoningStyle) Valid() bool {
	switch s {
	case ReasoningAuto, ReasoningNone, ReasoningAnthropic,
		ReasoningEffort, ReasoningOpenRouter, ReasoningQwen:
		return true
	}
	return false
}

// Provider is a resolved upstream: the row from the database with its API key
// decrypted. It exists only in memory, only for the duration of a request,
// and is never serialised anywhere.
type Provider struct {
	ID               string
	Name             string
	Kind             Kind
	BaseURL          string
	APIKey           string
	Headers          map[string]string
	AnthropicVersion string
	ReasoningStyle   ReasoningStyle
	Timeout          time.Duration
}

// ModelSpec is what the adapter needs to know about the model being called.
type ModelSpec struct {
	// The upstream identifier, e.g. "anthropic/claude-opus-5". Distinct from
	// the row id and from the name shown to users.
	ModelID           string
	SupportsReasoning bool
	SupportsImages    bool
	SupportsStreaming bool
	SupportsSystem    bool
	MaxOutputTokens   int
}

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	// What the caller's tools answered. Its own role because the protocols
	// disagree about where it belongs: OpenAI gives it a message of its own,
	// Anthropic makes it a block inside the next user turn. Folding it into
	// a user message here would pick Anthropic's answer for both.
	RoleTool Role = "tool"
)

type PartKind string

const (
	PartText  PartKind = "text"
	PartImage PartKind = "image"
	// A call the model asked for, replayed to it as part of the transcript.
	PartToolCall PartKind = "tool_call"
	// What that call returned.
	PartToolResult PartKind = "tool_result"
)

// Part is one piece of a message. Images travel as raw bytes plus a media
// type; each adapter encodes them the way its protocol wants, so nothing
// above this layer has to know that one wants a data URL and the other wants
// a base64 field.
type Part struct {
	Kind      PartKind
	Text      string
	MediaType string
	Data      []byte

	// Tool traffic, on PartToolCall and PartToolResult.
	//
	// ToolCallID is what pairs the two: the model chose it, the caller echoes
	// it back on the result, and both protocols refuse a result that does not
	// name a call they remember making. ToolName and ToolArgs describe the
	// call; a result carries its payload in Text.
	//
	// ToolArgs is the arguments object as JSON text, never decoded here. The
	// schema is the caller's, this layer has no opinion about it, and a
	// decode-and-re-encode is only ever a chance to change what the model
	// wrote.
	ToolCallID string
	ToolName   string
	ToolArgs   string
}

// Tool is one function the caller has offered the model.
type Tool struct {
	Name        string
	Description string
	// The JSON Schema for the arguments, forwarded exactly as it arrived.
	// Providers disagree about which keywords they accept, and rewriting a
	// schema to suit one of them is how a client's tool quietly stops
	// matching the function it actually implements.
	Parameters json.RawMessage
}

// ToolChoiceMode is how hard the caller wants the model pushed towards a tool.
type ToolChoiceMode string

const (
	// The model decides, which is what both protocols do when nothing is
	// said — so this is the zero value and nothing is sent for it.
	ToolChoiceAuto ToolChoiceMode = ""
	// The tools stay visible but must not be called.
	ToolChoiceNone ToolChoiceMode = "none"
	// Some tool must be called.
	ToolChoiceRequired ToolChoiceMode = "required"
	// This tool must be called.
	ToolChoiceNamed ToolChoiceMode = "named"
)

type ToolChoice struct {
	Mode ToolChoiceMode
	Name string
}

// ToolCall is one invocation the model asked for.
type ToolCall struct {
	ID   string
	Name string
	// The arguments object as JSON text, for the same reason Part.ToolArgs
	// is: it goes back to the caller byte for byte.
	Arguments string
}

type Message struct {
	Role  Role
	Parts []Part
}

type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
)

func (e Effort) Valid() bool { return e == EffortLow || e == EffortMedium || e == EffortHigh }

// Reasoning is the unified control. The frontend sends only this; it has no
// idea that budget_tokens or reasoning_effort exist.
//
// Effort is not necessarily one of the three above: a model may define its
// own tiers under its own names, and the gateway resolves what the client
// asked for against that model's list before handing it here. Whatever
// arrives has already been checked against the tiers the reader was offered.
type Reasoning struct {
	Enabled bool
	Effort  Effort
	// Anthropic's thinking budget, in tokens, when the model's tier names
	// one. Zero derives it from Effort, which is what an endpoint that only
	// understands reasoning_effort needs anyway.
	Budget int
}

type ChatRequest struct {
	Model       ModelSpec
	System      string
	Messages    []Message
	Temperature *float64
	MaxTokens   int
	Reasoning   Reasoning
	Stream      bool
	// What the caller offered the model, and how hard to push it.
	//
	// Not gated on a model capability flag. `supports_tools` exists on the
	// row and defaults to false on every model configured before this
	// worked, so refusing on it would ship a fix that stays broken
	// everywhere until an operator flips a switch they have never had a
	// reason to touch — and a model that genuinely cannot take tools says so
	// upstream, in a message the /v1 layer already renders.
	Tools      []Tool
	ToolChoice ToolChoice
	// Escape hatch for a provider parameter with no dedicated field. Merged
	// last, so an operator can override anything the adapter set.
	Extra map[string]any
}

type EventType int

const (
	// A piece of the answer.
	EventDelta EventType = iota
	// A piece of the model's reasoning.
	EventReasoning
	// Updated token counts. May arrive more than once.
	EventUsage
	// One tool call, whole.
	//
	// Whole rather than in pieces: both protocols stream the arguments as
	// JSON fragments, and half an arguments object is not something any
	// consumer can act on — it cannot even be parsed to find out whether it
	// is finished. The adapters accumulate the fragments and emit this once
	// the call is complete, which costs the caller nothing: an agent waits
	// for the whole call before running anything anyway.
	EventToolCall
)

type Event struct {
	Type     EventType
	Text     string
	Usage    Usage
	ToolCall ToolCall
}

type Usage struct {
	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
}

func (u Usage) Total() int { return u.InputTokens + u.OutputTokens + u.ReasoningTokens }

// Merge folds a later usage report into an earlier one. Anthropic reports
// input tokens at the start of a stream and output tokens at the end, so a
// naive overwrite would lose half the numbers.
func (u Usage) Merge(next Usage) Usage {
	if next.InputTokens > 0 {
		u.InputTokens = next.InputTokens
	}
	if next.OutputTokens > 0 {
		u.OutputTokens = next.OutputTokens
	}
	if next.ReasoningTokens > 0 {
		u.ReasoningTokens = next.ReasoningTokens
	}
	return u
}

type Result struct {
	Text      string
	Reasoning string
	// Every call the model asked for this turn, in the order it asked. More
	// than one is ordinary: both protocols let a model open several at once,
	// and an agent runs them in parallel.
	ToolCalls []ToolCall
	Usage     Usage
	Streamed  bool
	// Set when streaming was asked for but could not be used, naming why, so
	// the interface can say what happened instead of silently behaving
	// differently.
	StreamFallbackReason string
	FinishReason         string
}

// RemoteModel is one entry from a provider's own model listing.
type RemoteModel struct {
	ID          string
	DisplayName string
}

// Sink receives events as they arrive. Returning an error stops the stream —
// which is how a disconnected client cancels an upstream request.
type Sink func(Event) error

// ImageRequest is what an image generation call needs.
type ImageRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	Size           string `json:"size,omitempty"`
	Style          string `json:"style,omitempty"`
	Quality        string `json:"quality,omitempty"`
	N              int    `json:"n,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	// A picture the prompt works from. Present makes this an edit rather than
	// a generation, which is a different endpoint and a multipart body — so
	// these two never travel as JSON and carry no tags to suggest they might.
	Image     []byte `json:"-"`
	ImageMime string `json:"-"`
}

type GeneratedImage struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type ImageResult struct {
	Created int64            `json:"created"`
	Data    []GeneratedImage `json:"data"`
}

type Adapter interface {
	Kind() Kind
	Chat(ctx context.Context, client *http.Client, p Provider, req ChatRequest, sink Sink) (Result, error)
	ListModels(ctx context.Context, client *http.Client, p Provider) ([]RemoteModel, error)
	GenerateImage(ctx context.Context, client *http.Client, p Provider, req ImageRequest) (ImageResult, error)
}

// Registry holds the adapters and the one HTTP client they share.
//
// One client, because connection reuse across requests to the same provider
// is most of the latency difference on a busy instance, and because a
// per-request client leaks idle connections.
type Registry struct {
	client   *http.Client
	adapters map[Kind]Adapter
}

func NewRegistry(cfg config.Upstream) *Registry {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   cfg.DialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns: cfg.MaxIdleConns,
		// Per-host equals the whole pool because an instance usually talks to
		// one busy provider: a lower cap made UPSTREAM_MAX_IDLE_CONNS
		// unreachable and threw away every connection past the eighth, paying
		// a fresh TLS handshake for it on the next burst. The global ceiling
		// still bounds the total.
		MaxIdleConnsPerHost: cfg.MaxIdleConns,
		IdleConnTimeout:     cfg.IdleConnTimeout,
		// How long to wait for the first byte of the response. A generation
		// can take minutes, but a provider that has not even acknowledged the
		// request in this long is not going to.
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	}

	return &Registry{
		client: &http.Client{
			Transport: transport,
			// A provider request carries a bearer credential. Following a
			// redirect would copy it to the redirect target when Go considers
			// the hosts related (and non-standard credentials such as X-Api-Key
			// have even fewer built-in protections). Provider API endpoints are
			// expected to be final URLs, so make every redirect an ordinary
			// upstream response instead of a credential-forwarding hop.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
			// No client-level Timeout: it would cut a long streamed answer
			// off mid-sentence. The request context is the deadline, and it
			// is cancelled when the browser goes away.
			Timeout: cfg.RequestTimeout,
		},
		adapters: map[Kind]Adapter{
			KindOpenAI:    openAIAdapter{},
			KindAnthropic: anthropicAdapter{},
		},
	}
}

func (r *Registry) Chat(ctx context.Context, p Provider, req ChatRequest, sink Sink) (Result, error) {
	adapter, ok := r.adapters[p.Kind]
	if !ok {
		return Result{}, &Error{Kind: ErrorInvalidRequest, Message: "Unknown provider type " + string(p.Kind) + "."}
	}
	return adapter.Chat(ctx, r.client, p, req, sink)
}

func (r *Registry) ListModels(ctx context.Context, p Provider) ([]RemoteModel, error) {
	adapter, ok := r.adapters[p.Kind]
	if !ok {
		return nil, &Error{Kind: ErrorInvalidRequest, Message: "Unknown provider type " + string(p.Kind) + "."}
	}
	return adapter.ListModels(ctx, r.client, p)
}

func (r *Registry) GenerateImage(ctx context.Context, p Provider, req ImageRequest) (ImageResult, error) {
	adapter, ok := r.adapters[p.Kind]
	if !ok {
		return ImageResult{}, &Error{Kind: ErrorInvalidRequest, Message: "Unknown provider type " + string(p.Kind) + "."}
	}
	return adapter.GenerateImage(ctx, r.client, p, req)
}

func (r *Registry) Client() *http.Client { return r.client }

func (r *Registry) Kinds() []Kind { return []Kind{KindOpenAI, KindAnthropic} }

// resolveReasoningStyle turns "auto" into the protocol's own default. Kept
// here rather than in each adapter so the mapping is in one readable place.
func resolveReasoningStyle(p Provider) ReasoningStyle {
	if p.ReasoningStyle != ReasoningAuto && p.ReasoningStyle != "" {
		return p.ReasoningStyle
	}
	if p.Kind == KindAnthropic {
		return ReasoningAnthropic
	}
	return ReasoningEffort
}
