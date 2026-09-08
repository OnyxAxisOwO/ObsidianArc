package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

func testRegistry() *Registry {
	return NewRegistry(config.Upstream{
		DialTimeout:           2 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       time.Second,
	})
}

func testModel() ModelSpec {
	return ModelSpec{
		ModelID:           "test-model",
		SupportsReasoning: true,
		SupportsImages:    true,
		SupportsStreaming: true,
		SupportsSystem:    true,
		MaxOutputTokens:   4096,
	}
}

// collect drains a chat into the three things callers care about, so a test
// asserts on the outcome rather than on a callback protocol.
type collected struct {
	answer    strings.Builder
	reasoning strings.Builder
	usages    []Usage
	calls     []ToolCall
}

func (c *collected) sink(event Event) error {
	switch event.Type {
	case EventDelta:
		c.answer.WriteString(event.Text)
	case EventReasoning:
		c.reasoning.WriteString(event.Text)
	case EventUsage:
		c.usages = append(c.usages, event.Usage)
	case EventToolCall:
		c.calls = append(c.calls, event.ToolCall)
	}
	return nil
}

func sseServer(t *testing.T, frames []string, capture *map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			body, _ := io.ReadAll(r.Body)
			decoded := map[string]any{}
			_ = json.Unmarshal(body, &decoded)
			*capture = decoded
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, frame := range frames {
			fmt.Fprintf(w, "data: %s\n\n", frame)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// --- base URL ----------------------------------------------------------------

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		in       string
		insecure bool
		want     string
		ok       bool
	}{
		{in: "https://api.anthropic.com", want: "https://api.anthropic.com", ok: true},
		{in: "api.openai.com/v1", want: "https://api.openai.com/v1", ok: true},
		{in: "https://example.com/v1/", want: "https://example.com/v1", ok: true},
		{in: "http://localhost:11434/v1", want: "http://localhost:11434/v1", ok: true},
		{in: "http://127.0.0.1:8000", want: "http://127.0.0.1:8000", ok: true},
		// A key sent over plain http to a remote host is a key on the wire,
		// so that host needs the provider's opt-in and localhost does not.
		{in: "http://api.example.com", want: "", ok: false},
		{in: "http://198.51.100.7:8000/v1", insecure: true, want: "http://198.51.100.7:8000/v1", ok: true},
		// The opt-in widens http only: it does not make a bare address
		// default to http, and it does not admit some third scheme.
		{in: "api.example.com/v1", insecure: true, want: "https://api.example.com/v1", ok: true},
		{in: "ftp://api.example.com", insecure: true, want: "", ok: false},
		{in: "https://user:pass@api.example.com", want: "", ok: false},
		{in: "", want: "", ok: false},
		{in: "://nope", want: "", ok: false},
	}

	for _, tc := range cases {
		got, err := NormalizeBaseURL(tc.in, tc.insecure)
		if tc.ok && err != nil {
			t.Errorf("NormalizeBaseURL(%q, %v) failed: %v", tc.in, tc.insecure, err)
			continue
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("NormalizeBaseURL(%q, %v) accepted an unsafe URL", tc.in, tc.insecure)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeBaseURL(%q, %v) = %q, want %q", tc.in, tc.insecure, got, tc.want)
		}
	}
}

// Providers document the full endpoint, so that is what people paste. All
// three shapes have to land in the same place.
func TestEndpointResolution(t *testing.T) {
	for _, base := range []string{
		"https://api.anthropic.com",
		"https://api.anthropic.com/v1",
		"https://api.anthropic.com/v1/messages",
	} {
		if got := chatEndpoint(KindAnthropic, base); got != "https://api.anthropic.com/v1/messages" {
			t.Errorf("chatEndpoint(anthropic, %q) = %q", base, got)
		}
	}
	for _, base := range []string{
		"https://api.openai.com/v1",
		"https://api.openai.com/v1/chat/completions",
	} {
		if got := chatEndpoint(KindOpenAI, base); got != "https://api.openai.com/v1/chat/completions" {
			t.Errorf("chatEndpoint(openai, %q) = %q", base, got)
		}
	}
	if got := modelsEndpoint(KindOpenAI, "https://api.openai.com/v1/chat/completions"); got != "https://api.openai.com/v1/models" {
		t.Errorf("modelsEndpoint(openai) = %q", got)
	}
	if got := modelsEndpoint(KindAnthropic, "https://api.anthropic.com/v1/messages"); got != "https://api.anthropic.com/v1/models?limit=200" {
		t.Errorf("modelsEndpoint(anthropic) = %q", got)
	}
}

// --- inline reasoning ---------------------------------------------------------

func TestSplitThinking(t *testing.T) {
	cases := []struct {
		buffer    string
		reasoning string
		answer    string
	}{
		{"plain answer", "", "plain answer"},
		{"<think>weighing it up</think>the answer", "weighing it up", "the answer"},
		// A stream can stop anywhere, including inside the tag: everything
		// after an unclosed <think> is reasoning, because the answer has not
		// started.
		{"<think>still thinking", "still thinking", ""},
		{"<think>", "", ""},
		{"before<think>mid</think>after", "mid", "beforeafter"},
	}
	for _, tc := range cases {
		reasoning, answer := splitThinking(tc.buffer)
		if reasoning != tc.reasoning || answer != tc.answer {
			t.Errorf("splitThinking(%q) = (%q, %q), want (%q, %q)",
				tc.buffer, reasoning, answer, tc.reasoning, tc.answer)
		}
	}
}

func TestPartialThinkTagIsHeldBack(t *testing.T) {
	// "<th" could still become "<think>", so it must not be emitted as text
	// and then have to be taken back.
	if got := partialThinkTagLen("hello <th"); got != 3 {
		t.Errorf(`partialThinkTagLen("hello <th") = %d, want 3`, got)
	}
	if got := partialThinkTagLen("hello <think"); got != 6 {
		t.Errorf(`partialThinkTagLen("hello <think") = %d, want 6`, got)
	}
	if got := partialThinkTagLen("hello there"); got != 0 {
		t.Errorf("partialThinkTagLen on ordinary text = %d, want 0", got)
	}
}

// --- OpenAI-compatible --------------------------------------------------------

func TestOpenAIStreamSplitsInlineReasoning(t *testing.T) {
	frames := []string{
		`{"choices":[{"delta":{"content":"<th"}}]}`,
		`{"choices":[{"delta":{"content":"ink>let me check"}}]}`,
		`{"choices":[{"delta":{"content":"</think>The answer"}}]}`,
		`{"choices":[{"delta":{"content":" is 4."}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`,
	}
	server := sseServer(t, frames, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{Model: testModel(), Stream: true, Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
		out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if result.Text != "The answer is 4." {
		t.Errorf("answer = %q", result.Text)
	}
	if result.Reasoning != "let me check" {
		t.Errorf("reasoning = %q", result.Reasoning)
	}
	// The streamed pieces must add up to the same thing: a client that
	// concatenates deltas has to end where the final result does.
	if out.answer.String() != result.Text {
		t.Errorf("streamed answer %q != final %q", out.answer.String(), result.Text)
	}
	if out.reasoning.String() != result.Reasoning {
		t.Errorf("streamed reasoning %q != final %q", out.reasoning.String(), result.Reasoning)
	}
	if result.Usage.InputTokens != 11 || result.Usage.OutputTokens != 7 {
		t.Errorf("usage = %+v", result.Usage)
	}
	if !result.Streamed {
		t.Error("result is not marked as streamed")
	}
}

func TestOpenAIStreamUsesReasoningField(t *testing.T) {
	frames := []string{
		`{"choices":[{"delta":{"reasoning_content":"thinking hard"}}]}`,
		`{"choices":[{"delta":{"content":"done"}}]}`,
		`{"usage":{"prompt_tokens":3,"completion_tokens":2,"completion_tokens_details":{"reasoning_tokens":9}}}`,
	}
	server := sseServer(t, frames, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{Model: testModel(), Stream: true, Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
		out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if result.Reasoning != "thinking hard" || result.Text != "done" {
		t.Errorf("result = %+v", result)
	}
	if result.Usage.ReasoningTokens != 9 {
		t.Errorf("reasoning tokens = %d, want 9", result.Usage.ReasoningTokens)
	}
}

// The frontend only ever sends {enabled, effort}. Which flag that becomes is
// the provider's business, and this is where the translation is pinned down.
func TestReasoningStyleTranslation(t *testing.T) {
	cases := []struct {
		style  ReasoningStyle
		assert func(t *testing.T, body map[string]any)
	}{
		{ReasoningEffort, func(t *testing.T, body map[string]any) {
			if body["reasoning_effort"] != "high" {
				t.Errorf("reasoning_effort = %v", body["reasoning_effort"])
			}
		}},
		{ReasoningOpenRouter, func(t *testing.T, body map[string]any) {
			nested, ok := body["reasoning"].(map[string]any)
			if !ok || nested["effort"] != "high" {
				t.Errorf("reasoning = %v", body["reasoning"])
			}
		}},
		{ReasoningQwen, func(t *testing.T, body map[string]any) {
			if body["enable_thinking"] != true {
				t.Errorf("enable_thinking = %v", body["enable_thinking"])
			}
		}},
		{ReasoningNone, func(t *testing.T, body map[string]any) {
			for _, key := range []string{"reasoning_effort", "reasoning", "enable_thinking"} {
				if _, present := body[key]; present {
					t.Errorf("style=none still sent %s", key)
				}
			}
		}},
	}

	for _, tc := range cases {
		var body map[string]any
		server := sseServer(t, []string{`{"choices":[{"delta":{"content":"ok"}}]}`}, &body)

		var out collected
		_, err := testRegistry().Chat(context.Background(),
			Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k", ReasoningStyle: tc.style},
			ChatRequest{
				Model:     testModel(),
				Stream:    true,
				Reasoning: Reasoning{Enabled: true, Effort: EffortHigh},
				Messages:  []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
			}, out.sink)
		if err != nil {
			t.Fatalf("style %s: %v", tc.style, err)
		}
		tc.assert(t, body)
	}
}

// --- Anthropic ----------------------------------------------------------------

func TestAnthropicStreamSeparatesThinking(t *testing.T) {
	frames := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":25}}}`,
		`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"weighing"}}`,
		`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":" it up"}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"Four."}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`,
	}
	server := sseServer(t, frames, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindAnthropic, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{Model: testModel(), Stream: true, Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
		out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if result.Text != "Four." || result.Reasoning != "weighing it up" {
		t.Errorf("result = %+v", result)
	}
	// Input tokens arrive at the start and output tokens at the end, so a
	// naive overwrite would lose half of them.
	if result.Usage.InputTokens != 25 || result.Usage.OutputTokens != 8 {
		t.Errorf("usage = %+v, want input 25 / output 8", result.Usage)
	}
	if result.FinishReason != "end_turn" {
		t.Errorf("finish reason = %q", result.FinishReason)
	}
}

func TestAnthropicThinkingBudgetAndTemperature(t *testing.T) {
	var body map[string]any
	server := sseServer(t, []string{`{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}`}, &body)

	temperature := 0.7
	var out collected
	_, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindAnthropic, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model:       testModel(),
			Stream:      true,
			Temperature: &temperature,
			Reasoning:   Reasoning{Enabled: true, Effort: EffortHigh},
			System:      "be brief",
			Messages:    []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
		}, out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	thinking, ok := body["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking = %v", body["thinking"])
	}
	if budget, _ := thinking["budget_tokens"].(float64); int(budget) != budgetHigh {
		t.Errorf("budget_tokens = %v, want %d", thinking["budget_tokens"], budgetHigh)
	}
	// The API rejects a temperature other than 1 while thinking is on, so the
	// user's value has to be dropped rather than passed through.
	if _, present := body["temperature"]; present {
		t.Error("temperature was sent alongside extended thinking")
	}
	// max_tokens has to leave room for an answer after the reasoning.
	if maxTokens, _ := body["max_tokens"].(float64); int(maxTokens) < budgetHigh+budgetHeadroom {
		t.Errorf("max_tokens = %v, want at least %d", body["max_tokens"], budgetHigh+budgetHeadroom)
	}
	// Anthropic carries the system prompt beside the messages, not as one.
	if body["system"] != "be brief" {
		t.Errorf("system = %v", body["system"])
	}
}

// Editing a message mid-transcript can leave two user turns in a row, or an
// assistant turn first. Both are rejected by both providers.
func TestTranscriptIsRepaired(t *testing.T) {
	messages := []Message{
		{Role: RoleAssistant, Parts: []Part{{Kind: PartText, Text: "orphan"}}},
		{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "first"}}},
		{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "second"}}},
		{Role: RoleAssistant, Parts: []Part{{Kind: PartText, Text: "reply"}}},
	}

	openAI, _ := openAIAdapter{}.buildMessages(ChatRequest{Model: testModel(), Messages: messages})
	if len(openAI) != 2 {
		t.Fatalf("openai messages = %d, want 2: %+v", len(openAI), openAI)
	}
	if openAI[0].Role != "user" {
		t.Errorf("openai list starts with %q", openAI[0].Role)
	}
	if content, _ := openAI[0].Content.(string); content != "first\n\nsecond" {
		t.Errorf("adjacent user turns were not merged: %q", content)
	}

	anthropic, _ := anthropicAdapter{}.buildMessages(ChatRequest{Model: testModel(), Messages: messages})
	if len(anthropic) != 2 || anthropic[0].Role != "user" {
		t.Fatalf("anthropic messages = %+v", anthropic)
	}
}

func TestImagesAreDroppedForTextOnlyModels(t *testing.T) {
	spec := testModel()
	spec.SupportsImages = false

	request := ChatRequest{
		Model: spec,
		Messages: []Message{{Role: RoleUser, Parts: []Part{
			{Kind: PartText, Text: "what is this"},
			{Kind: PartImage, MediaType: "image/png", Data: []byte{1, 2, 3}},
		}}},
	}

	_, openAICarried := openAIAdapter{}.buildMessages(request)
	if openAICarried {
		t.Error("openai adapter sent an image to a text-only model")
	}
	_, anthropicCarried := anthropicAdapter{}.buildMessages(request)
	if anthropicCarried {
		t.Error("anthropic adapter sent an image to a text-only model")
	}
}

// --- errors --------------------------------------------------------------------

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		body   string
		images bool
		want   ErrorKind
	}{
		{401, `{"error":{"message":"invalid api key"}}`, false, ErrorAuth},
		{403, `{"error":{"message":"forbidden"}}`, false, ErrorAuth},
		{429, `{"error":{"message":"slow down"}}`, false, ErrorRateLimit},
		{500, `{"error":{"message":"boom"}}`, false, ErrorUpstream},
		{400, `{"error":{"message":"bad"}}`, false, ErrorInvalidRequest},
		// A text-only model rejects an image by naming the wire format; the
		// cause is that it cannot see.
		{400, `{"error":{"message":"invalid content type"}}`, true, ErrorImagesUnsupported},
	}

	for _, tc := range cases {
		err := classifyHTTP(
			Provider{Kind: KindOpenAI, Name: "Test"},
			"https://api.example.com/v1/chat/completions",
			tc.status, "", []byte(tc.body), tc.images)
		if err.Kind != tc.want {
			t.Errorf("status %d (images=%v) classified as %s, want %s", tc.status, tc.images, err.Kind, tc.want)
		}
	}
}

// An auth failure almost never means a typo; it means the key belongs to a
// different service than the provider is configured as. The message has to
// say which, and where.
func TestAuthErrorNamesProviderAndEndpoint(t *testing.T) {
	err := classifyHTTP(
		Provider{Kind: KindAnthropic, Name: "Test"},
		"https://api.openai.com/v1/messages",
		401, "", []byte(`{"error":{"message":"invalid x-api-key"}}`), false)

	if !strings.Contains(err.Message, "anthropic") {
		t.Errorf("message does not name the provider kind: %q", err.Message)
	}
	if !strings.Contains(err.Message, "api.openai.com") {
		t.Errorf("message does not name the endpoint: %q", err.Message)
	}
}

func TestRetryAfterIsParsed(t *testing.T) {
	err := classifyHTTP(Provider{Kind: KindOpenAI}, "https://x/y", 429, "30", nil, false)
	if err.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", err.RetryAfter)
	}
}

// A base URL is shown to an administrator to explain where a key went, so
// anything credential-shaped in it has to go first.
func TestRedactURLStripsCredentials(t *testing.T) {
	got := redactURL("https://user:secret@api.example.com/v1?token=abc")
	if strings.Contains(got, "secret") || strings.Contains(got, "abc") {
		t.Errorf("redactURL leaked a credential: %q", got)
	}
}

// --- fallback and cancellation --------------------------------------------------

// A server that answers a stream request with a document should still produce
// an answer, and should say why it did not arrive progressively.
func TestNonStreamingResponseFallsBack(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"all at once"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":4,"completion_tokens":3}}`)
	}))
	defer server.Close()

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{Model: testModel(), Stream: true, Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
		out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if result.Text != "all at once" {
		t.Errorf("answer = %q", result.Text)
	}
	if result.Streamed {
		t.Error("a one-shot answer was reported as streamed")
	}
	if result.StreamFallbackReason == "" {
		t.Error("no reason was given for the fallback")
	}
	// The caller's streaming path is the only path, so the whole answer still
	// arrives through the sink.
	if out.answer.String() != "all at once" {
		t.Errorf("sink received %q", out.answer.String())
	}
}

// Stop, and a disconnected browser, both surface as the sink refusing more
// events. Whatever streamed before that is kept, because it is what the user
// read while deciding to stop.
func TestSinkErrorStopsTheStreamAndKeepsPartialText(t *testing.T) {
	frames := []string{
		`{"choices":[{"delta":{"content":"one "}}]}`,
		`{"choices":[{"delta":{"content":"two "}}]}`,
		`{"choices":[{"delta":{"content":"three"}}]}`,
	}
	server := sseServer(t, frames, nil)

	stop := errors.New("client went away")
	seen := 0
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{Model: testModel(), Stream: true, Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
		func(event Event) error {
			if event.Type != EventDelta {
				return nil
			}
			seen++
			if seen == 2 {
				return stop
			}
			return nil
		})

	if !errors.Is(err, stop) {
		t.Fatalf("want the sink's own error back, got %v", err)
	}
	if result.Text == "" {
		t.Error("the text streamed before the stop was discarded")
	}
}

// A cancelled context must not be reported as a provider failure: the user
// pressed Stop, and nothing is wrong.
func TestCancellationIsNotAnUpstreamError(t *testing.T) {
	// Never answers. The upper bound is a backstop so a platform that is slow
	// to surface a closed connection cannot wedge the suite: the assertion
	// below depends on the client's own cancellation, not on this timer.
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	var out collected
	_, err := testRegistry().Chat(ctx,
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{Model: testModel(), Stream: true, Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
		out.sink)

	var upstream *Error
	if !errors.As(err, &upstream) {
		t.Fatalf("want an adapter error, got %v", err)
	}
	if upstream.Kind != ErrorCancelled {
		t.Errorf("cancellation classified as %s", upstream.Kind)
	}
}

// --- credentials ----------------------------------------------------------------

// Each protocol carries the key in its own header, and neither should be
// reachable from the provider's configurable extras.
func TestCredentialHeaders(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}],"usage":{}}`)
	}))
	defer server.Close()

	var out collected
	_, err := testRegistry().Chat(context.Background(),
		Provider{
			Kind:    KindAnthropic,
			BaseURL: server.URL,
			APIKey:  "sk-real-key",
			// An operator cannot redirect the credential by adding a header.
			Headers: map[string]string{"X-Api-Key": "hijacked", "X-Trace": "on"},
		},
		ChatRequest{Model: testModel(), Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
		out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if got.Get("X-Api-Key") != "sk-real-key" {
		t.Errorf("X-Api-Key = %q", got.Get("X-Api-Key"))
	}
	if got.Get("Anthropic-Version") != defaultAnthropicVersion {
		t.Errorf("Anthropic-Version = %q", got.Get("Anthropic-Version"))
	}
	if got.Get("X-Trace") != "on" {
		t.Error("a harmless extra header was dropped")
	}
	if got.Get("Authorization") != "" {
		t.Error("a bearer token was sent to an Anthropic endpoint")
	}
}

// A compromised endpoint must not be able to bounce a provider credential
// to another origin. Go normally follows redirects and considers a different
// port on the same host close enough to inherit sensitive headers.
func TestProviderRedirectDoesNotForwardCredentials(t *testing.T) {
	var redirected http.Header
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"stolen"}],"usage":{}}`)
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	var out collected
	_, _ = testRegistry().Chat(context.Background(), Provider{
		Kind: KindAnthropic, BaseURL: source.URL, APIKey: "sk-must-stay-at-source",
	}, ChatRequest{
		Model:    testModel(),
		Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
	}, out.sink)

	if redirected != nil {
		t.Fatalf("redirect target was contacted with headers: %v", redirected)
	}
}

func TestListModelsHandlesBothShapes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"a","display_name":"Model A"},{"id":"b"},{"id":""}]}`)
	}))
	defer server.Close()

	models, err := testRegistry().ListModels(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2 (the blank id should be dropped): %+v", len(models), models)
	}
	if models[0].DisplayName != "Model A" || models[1].DisplayName != "" {
		t.Errorf("models = %+v", models)
	}
}

func TestImagesEndpointResolution(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://api.openai.com", "https://api.openai.com/v1/images/generations"},
		{"https://api.openai.com/v1", "https://api.openai.com/v1/images/generations"},
		{"https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1/images/generations"},
		{"https://api.openai.com/v1/images/generations", "https://api.openai.com/v1/images/generations"},
	}
	for _, tc := range cases {
		if got := imagesEndpoint(tc.in); got != tc.want {
			t.Errorf("imagesEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGenerateImageOpenAIAndAnthropic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["prompt"] != "a cute cat" {
			http.Error(w, "bad prompt", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"created":12345,"data":[{"b64_json":"aGVsbG8=","revised_prompt":"a very cute cat"}]}`)
	}))
	defer server.Close()

	reg := testRegistry()
	res, err := reg.GenerateImage(context.Background(), Provider{
		Kind: KindOpenAI, BaseURL: server.URL, APIKey: "sk-test",
	}, ImageRequest{
		Model:  "dall-e-3",
		Prompt: "a cute cat",
	})
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if len(res.Data) != 1 || res.Data[0].B64JSON != "aGVsbG8=" || res.Data[0].RevisedPrompt != "a very cute cat" {
		t.Errorf("res = %+v", res)
	}

	_, err = reg.GenerateImage(context.Background(), Provider{
		Kind: KindAnthropic, BaseURL: server.URL, APIKey: "sk-test",
	}, ImageRequest{
		Model:  "claude-3-5",
		Prompt: "a cute cat",
	})
	if err == nil {
		t.Fatal("expected error for anthropic image generation")
	}
}
