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
	"testing/iotest"
	"time"
)

func rawStream(body string) *http.Response {
	return &http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func streamData(frames ...string) string {
	return "data: " + strings.Join(frames, "\n\ndata: ") + "\n\n"
}

func TestProtocolUsageCountsEachTokenOnce(t *testing.T) {
	var openai openAIUsage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":100,"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":70},"completion_tokens_details":{"reasoning_tokens":30}}`), &openai); err != nil {
		t.Fatal(err)
	}
	u := openai.toUsage()
	if u.InputTokens != 100 || u.OutputTokens != 10 || u.ReasoningTokens != 30 || u.CacheReadTokens != 70 || u.Total() != 140 {
		t.Fatalf("OpenAI usage = %+v", u)
	}
	var anthropic anthropicTokenUsage
	if err := json.Unmarshal([]byte(`{"input_tokens":0,"output_tokens":5,"cache_read_input_tokens":900,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":20,"ephemeral_1h_input_tokens":80}}`), &anthropic); err != nil {
		t.Fatal(err)
	}
	u = fillUsage(userSays("hi"), Result{Text: "hello"}, anthropic.toUsage())
	if u.InputTokens != 1000 || u.Total() != 1005 || u.Estimated || u.CacheCreationTokens != 100 {
		t.Fatalf("Anthropic usage = %+v", u)
	}
	openai.CompletionTokensDetails.ReasoningTokens = 40
	u = fillUsage(userSays("hi"), Result{Reasoning: "all thinking"}, openai.toUsage())
	if u.OutputTokens != 0 || u.Total() != 140 || u.Estimated {
		t.Fatalf("reasoning-only output was estimated again: %+v", u)
	}
}

func TestMultilineSSEAndLineEndings(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		body := "\ufeff: ping" + ending + "event: message" + ending + "data: {\"choices\":" + ending + "data: [{\"delta\":{\"content\":\"你好🙂\"}}]}" + ending + ending + "data: [DONE]" + ending + ending
		var out collected
		response := rawStream(body)
		response.Body = io.NopCloser(iotest.OneByteReader(strings.NewReader(body)))
		result, err := (openAIAdapter{}).readStream(context.Background(), response, out.sink, "")
		if err != nil || result.Text != "你好🙂" || out.answer.String() != result.Text {
			t.Fatalf("ending %q: result=%+v err=%v", ending, result, err)
		}
	}
}

func TestStreamDamageNeverBecomesSuccess(t *testing.T) {
	cases := []struct {
		kind   Kind
		frames []string
	}{
		{KindOpenAI, []string{`{"choices":[{"delta":{"content":"part"}}]}`}},
		{KindOpenAI, []string{`{"choices":[{"delta":{"content":"part"}}]}`, `{broken`, "[DONE]"}},
		{KindOpenAI, []string{`{"choices":[{"delta":{"content":"part"}}]}`, `{}`, "[DONE]"}},
		{KindAnthropic, []string{`{"type":"content_block_delta","delta":{"type":"text_delta","text":"part"}}`, `{broken`, `{"type":"message_stop"}`}},
		{KindAnthropic, []string{`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"a","name":"run"}}`, `{"type":"message_stop"}`}},
	}
	for i, tc := range cases {
		var result Result
		var err error
		var out collected
		response := rawStream(streamData(tc.frames...))
		if tc.kind == KindOpenAI {
			result, err = (openAIAdapter{}).readStream(context.Background(), response, out.sink, "")
		} else {
			result, err = (anthropicAdapter{}).readStream(context.Background(), response, out.sink, "")
		}
		var upstream *Error
		if !errors.As(err, &upstream) || upstream.Kind != ErrorUpstream {
			t.Fatalf("case %d: result=%+v err=%v", i, result, err)
		}
		if len(result.ToolCalls) > 0 {
			t.Fatalf("case %d: unfinished tool emitted", i)
		}
		if i < 4 && result.Text != "part" {
			t.Fatalf("case %d lost partial text: %+v", i, result)
		}
	}
}

func TestProtocolTerminatorDoesNotWaitForEOF(t *testing.T) {
	for _, kind := range []Kind{KindOpenAI, KindAnthropic} {
		t.Run(string(kind), func(t *testing.T) {
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			response := &http.Response{Body: reader}
			done := make(chan error, 1)
			go func() {
				var err error
				if kind == KindOpenAI {
					_, err = (openAIAdapter{}).readStream(context.Background(), response, func(Event) error { return nil }, "")
				} else {
					_, err = (anthropicAdapter{}).readStream(context.Background(), response, func(Event) error { return nil }, "")
				}
				done <- err
			}()
			frame := "[DONE]"
			if kind == KindAnthropic {
				frame = `{"type":"message_stop"}`
			}
			if _, err := io.WriteString(writer, streamData(frame)); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("waited for EOF after terminal event")
			}
		})
	}
}

func TestOpenAIToolsFinishBeforeTrailingUsageAndKeepWireOrder(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	calls := make(chan ToolCall, 2)
	done := make(chan error, 1)
	go func() {
		_, err := (openAIAdapter{}).readStream(context.Background(), &http.Response{Body: reader}, func(event Event) error {
			if event.Type == EventToolCall {
				calls <- event.ToolCall
			}
			return nil
		}, "")
		done <- err
	}()
	frames := []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"second","arguments":"{}"}},{"index":0,"id":"a","function":{"name":"read_","arguments":"{"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"file","arguments":"}"}}]},"finish_reason":"tool_calls"}]}`,
	}
	if _, err := io.WriteString(writer, streamData(frames...)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"read_file", "second"} {
		select {
		case call := <-calls:
			if call.Name != want || call.Arguments != "{}" {
				t.Fatalf("call=%+v want %s", call, want)
			}
		case <-time.After(time.Second):
			t.Fatal("tool held until EOF/usage")
		}
	}
	if _, err := io.WriteString(writer, streamData(`{"usage":{"prompt_tokens":2,"completion_tokens":3}}`, "[DONE]")); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAnthropicThinkingHonoursOutgoingCeiling(t *testing.T) {
	for _, ceiling := range []int{500, 1000, 2048, 4096, 32000} {
		req := userSays("hi")
		req.MaxTokens = ceiling
		req.Model.MaxOutputTokens = ceiling
		req.Reasoning = Reasoning{Enabled: true, Effort: EffortHigh}
		body, _ := (anthropicAdapter{}).buildBody(Provider{Kind: KindAnthropic}, req)
		if body["max_tokens"] != ceiling {
			t.Fatalf("ceiling %d -> %v", ceiling, body)
		}
		if thinking, ok := body["thinking"].(map[string]any); ok && thinking["budget_tokens"].(int) >= ceiling {
			t.Fatalf("invalid thinking budget: %v", body)
		}
	}
}

func TestStreamedRefusalKeepsTextAndReportsFailure(t *testing.T) {
	var out collected
	result, err := (openAIAdapter{}).readStream(context.Background(), rawStream(streamData(
		`{"choices":[{"delta":{"refusal":"Cannot "}}]}`,
		`{"choices":[{"delta":{"refusal":"comply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`, "[DONE]")), out.sink, "")
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Kind != ErrorRefusal || result.Text != "Cannot comply" || out.answer.String() != result.Text || result.Usage.Total() != 7 {
		t.Fatalf("result=%+v err=%v events=%s", result, err, out.answer.String())
	}
}

func TestAnthropicCacheUsageAndInitialContent(t *testing.T) {
	var out collected
	result, err := (anthropicAdapter{}).readStream(context.Background(), rawStream(streamData(
		`{"type":"message_start","message":{"usage":{"input_tokens":0,"cache_read_input_tokens":900,"cache_creation_input_tokens":100,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"first"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" last"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"stop_sequence","stop_sequence":"END"},"usage":{"input_tokens":5,"output_tokens":5}}`,
		`{"type":"message_stop"}`)), out.sink, "")
	if err != nil || result.Text != "first last" || result.Usage.Total() != 1010 || result.StopSequence == nil || *result.StopSequence != "END" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestStreamIdleTimeoutClosesStalledBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, streamData(`{"choices":[{"delta":{"content":"partial"}}]}`))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	registry := testRegistry()
	registry.streamIdleTimeout = 40 * time.Millisecond
	result, err := registry.Chat(context.Background(), Provider{Kind: KindOpenAI, BaseURL: server.URL}, userSays("hi"), func(Event) error { return nil })
	var timeout *Error
	if !errors.As(err, &timeout) || timeout.Kind != ErrorNetwork || result.Text != "partial" || !result.Usage.Estimated {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCacheBreakpointsAndToolErrorsSurviveNativeBuild(t *testing.T) {
	cache := &CacheControl{Type: "ephemeral", TTL: "1h"}
	req := userSays("before")
	req.Messages[0].Parts = append(req.Messages[0].Parts, Part{Kind: PartText, Text: "prefix", CacheControl: cache}, Part{Kind: PartText, Text: "after"})
	req.System = "system"
	req.SystemParts = []Part{{Kind: PartText, Text: "system", CacheControl: cache}}
	req.Tools = []Tool{{Name: "run", CacheControl: cache}}
	req.Messages = append(req.Messages, Message{Role: RoleAssistant, Parts: []Part{{Kind: PartToolCall, ToolName: "run", ToolCallID: "a", ToolArgs: "{}"}}}, Message{Role: RoleTool, Parts: []Part{{Kind: PartToolResult, ToolCallID: "a", Text: "failed", ToolIsError: true}}})
	body, _ := (anthropicAdapter{}).buildBody(Provider{Kind: KindAnthropic}, req)
	raw, _ := json.Marshal(body)
	for _, expected := range []string{`"text":"prefix"`, `"ttl":"1h"`, `"is_error":true`, `"text":"after"`} {
		if !strings.Contains(string(raw), expected) {
			t.Fatalf("missing %s: %s", expected, raw)
		}
	}
	messages := body["messages"].([]anthropicMessage)
	if len(messages[0].Content.([]any)) != 3 {
		t.Fatalf("cached text blocks were merged: %s", raw)
	}
}

func TestToolCallDoesNotArriveAfterTheProseThatFollowedIt(t *testing.T) {
	var order []string
	result, err := (openAIAdapter{}).readStream(context.Background(), rawStream(streamData(
		`{"choices":[{"delta":{"content":"before"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"run","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{"content":"after"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		"[DONE]",
	)), func(event Event) error {
		switch event.Type {
		case EventDelta:
			order = append(order, event.Text)
		case EventToolCall:
			order = append(order, event.ToolCall.Name)
		}
		return nil
	}, "")
	if err != nil || strings.Join(order, ",") != "before,run,after" || result.Text != "beforeafter" {
		t.Fatalf("order=%v result=%+v err=%v", order, result, err)
	}
}

func TestOpenAIUsageKeepsAdditiveDialectAndExplicitFreeResponses(t *testing.T) {
	for _, tc := range []struct {
		body                  string
		output, reason, total int
	}{
		{`{"prompt_tokens":3,"completion_tokens":11,"total_tokens":14,"completion_tokens_details":{"reasoning_tokens":9}}`, 2, 9, 14},
		{`{"prompt_tokens":3,"completion_tokens":2,"total_tokens":14,"completion_tokens_details":{"reasoning_tokens":9}}`, 2, 9, 14},
		{`{"prompt_tokens":3,"completion_tokens":2,"completion_tokens_details":{"reasoning_tokens":9}}`, 2, 9, 14},
		{`{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`, 0, 0, 0},
	} {
		var wire openAIUsage
		if err := json.Unmarshal([]byte(tc.body), &wire); err != nil {
			t.Fatal(err)
		}
		usage := fillUsage(userSays("hi"), Result{Text: "cached answer"}, wire.toUsage())
		if usage.OutputTokens != tc.output || usage.ReasoningTokens != tc.reason || usage.Total() != tc.total || usage.Estimated {
			t.Fatalf("%s => %+v", tc.body, usage)
		}
	}
}

func TestPartialCacheCreationReportsKeepBothTTLBuckets(t *testing.T) {
	var first, second anthropicTokenUsage
	_ = json.Unmarshal([]byte(`{"input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":200}}`), &first)
	_ = json.Unmarshal([]byte(`{"cache_creation":{"ephemeral_1h_input_tokens":80},"output_tokens":5}`), &second)
	usage := first.merge(second).toUsage()
	if usage.CacheCreation5mTokens != 200 || usage.CacheCreation1hTokens != 80 || usage.InputTokens != 280 || usage.Total() != 285 {
		t.Fatal(usage)
	}
}

func TestFragmentedSSEOverTLSHTTP1AndHTTP2(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%v", http2), func(t *testing.T) {
			cancelled := make(chan struct{})
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := 1
				if http2 {
					want = 2
				}
				if r.ProtoMajor != want {
					t.Errorf("protocol=%s", r.Proto)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				body := streamData(`{"choices":[{"delta":{"content":"中文🙂"}}]}`, "[DONE]")
				for i := 0; i < len(body); i++ {
					if _, err := w.Write([]byte{body[i]}); err != nil {
						return
					}
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
				close(cancelled)
			}))
			server.EnableHTTP2 = http2
			server.StartTLS()
			defer server.Close()
			registry := testRegistry()
			registry.client = server.Client()
			var out collected
			result, err := registry.Chat(context.Background(), Provider{Kind: KindOpenAI, BaseURL: server.URL}, userSays("hi"), out.sink)
			if err != nil || result.Text != "中文🙂" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			select {
			case <-cancelled:
			case <-time.After(2 * time.Second):
				t.Fatal("completed stream did not release its network body")
			}
		})
	}
}

func TestEmulatedToolsDoNotParseRefusalAsACommand(t *testing.T) {
	const refused = `<tool_call>{"name":"run","arguments":{}}</tool_call>`
	encoded, _ := json.Marshal(refused)
	server := sseServer(t, []string{`{"choices":[{"delta":{"refusal":` + string(encoded) + `}}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":7}}`}, nil)
	req := userSays("hi")
	req.Model.EmulateTools = true
	req.Tools = []Tool{{Name: "run"}}
	var calls int
	var refusal strings.Builder
	result, err := testRegistry().Chat(context.Background(), Provider{Kind: KindOpenAI, BaseURL: server.URL}, req, func(event Event) error {
		if event.Type == EventToolCall {
			calls++
		}
		if event.Refusal {
			refusal.WriteString(event.Text)
		}
		return nil
	})
	var declined *Error
	if !errors.As(err, &declined) || declined.Kind != ErrorRefusal || calls != 0 || result.Refusal != refused || refusal.String() != refused {
		t.Fatalf("result=%+v err=%v calls=%d refusal=%s", result, err, calls, refusal.String())
	}
	transcript := rewriteToolTranscript([]Message{{Role: RoleTool, Parts: []Part{{Kind: PartToolResult, Text: "failed", ToolIsError: true}}}})
	if !strings.Contains(transcript[0].Parts[0].Text, "[tool_error]") {
		t.Fatal(transcript)
	}
}

func TestLowThinkingCeilingKeepsForcedToolChoiceWhenThinkingIsOff(t *testing.T) {
	req := userSays("hi")
	req.MaxTokens = 1000
	req.Reasoning = Reasoning{Enabled: true, Effort: EffortHigh}
	req.Tools = []Tool{{Name: "run"}}
	req.ToolChoice = ToolChoice{Mode: ToolChoiceNamed, Name: "run"}
	body, _ := (anthropicAdapter{}).buildBody(Provider{Kind: KindAnthropic}, req)
	if body["thinking"] != nil || body["tool_choice"].(map[string]any)["name"] != "run" || body["max_tokens"] != 1000 {
		t.Fatal(body)
	}
}
