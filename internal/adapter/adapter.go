// Package adapter is the only place in this server that knows what an AI
// provider's wire format looks like.
//
// Everything above it — the chat gateway, the usage ledger, the handlers —
// speaks one request shape and consumes one event stream. There is no
// `if kind == "openai"` outside this package, and adding a third protocol is
// a new file here plus a row in the registry, not a change anywhere else.
//
// The shapes below are deliberately smaller than what either provider can
// express. This is a chat server: text and images in, text and reasoning out.
// Tool calls, structured output and the rest are absent because nothing above
// this layer has anywhere to put them yet, and a field with no consumer is a
// field that drifts.
package adapter

import (
	"context"
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
)

type PartKind string

const (
	PartText  PartKind = "text"
	PartImage PartKind = "image"
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
type Reasoning struct {
	Enabled bool
	Effort  Effort
}

type ChatRequest struct {
	Model       ModelSpec
	System      string
	Messages    []Message
	Temperature *float64
	MaxTokens   int
	Reasoning   Reasoning
	Stream      bool
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
)

type Event struct {
	Type  EventType
	Text  string
	Usage Usage
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

type Adapter interface {
	Kind() Kind
	Chat(ctx context.Context, client *http.Client, p Provider, req ChatRequest, sink Sink) (Result, error)
	ListModels(ctx context.Context, client *http.Client, p Provider) ([]RemoteModel, error)
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
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: 8,
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
