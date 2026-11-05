package compat

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

func nativeProvider(f *fixture, t *testing.T, kind string) {
	t.Helper()
	if _, err := f.db.Exec(context.Background(), `UPDATE providers SET kind = ? WHERE id = ?`, kind, f.model.ProviderID); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveredRefusalKeepsNativeContentAndBillsOnce(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		for _, stream := range []bool{false, true} {
			for _, upstreamStream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%v/upstream=%v", endpoint, stream, upstreamStream), func(t *testing.T) {
					f := newFixture(t)
					if upstreamStream {
						f.upstream.stream(`{"choices":[{"delta":{"refusal":"Cannot "}}]}`, `{"choices":[{"delta":{"refusal":"comply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`)
					} else {
						f.upstream.reply(`{"choices":[{"message":{"content":null,"refusal":"Cannot comply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`)
					}
					w := f.do(t, http.MethodPost, endpoint, f.token, protocolBody(f.model.ID, endpoint, stream))
					raw := w.Body.String()
					if w.Code != 200 || strings.Contains(raw, `"code":"refusal"`) {
						t.Fatalf("status=%d body=%s", w.Code, raw)
					}
					switch endpoint {
					case "/v1/chat/completions":
						if !strings.Contains(raw, `"refusal":"Cannot`) || strings.Contains(raw, `"content":"Cannot`) {
							t.Fatalf("refusal lost its native field: %s", raw)
						}
						if !stream && !strings.Contains(raw, `"content":null`) {
							t.Fatal(raw)
						}
					case "/v1/responses":
						if !strings.Contains(raw, `"type":"refusal"`) || !strings.Contains(raw, `"refusal":"Cannot comply"`) {
							t.Fatalf("missing refusal part: %s", raw)
						}
						if stream && (!strings.Contains(raw, "event: response.refusal.delta") || !strings.Contains(raw, "event: response.refusal.done") || !strings.Contains(raw, "event: response.completed")) {
							t.Fatal(raw)
						}
					case "/v1/messages":
						if !strings.Contains(raw, `"stop_reason":"refusal"`) {
							t.Fatal(raw)
						}
					}
					records := f.turns()
					if len(records) != 1 || records[0].ErrorCode != "refusal" || records[0].Usage.Total() != 7 || records[0].Usage.Estimated {
						t.Fatalf("records=%+v", records)
					}
				})
			}
		}
	}
}

func TestNativeAnthropicStopReasonsAcrossSurfaces(t *testing.T) {
	for _, reason := range []string{"end_turn", "stop_sequence", "model_context_window_exceeded", "pause_turn", "refusal", "max_tokens"} {
		for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%v", reason, endpoint, stream), func(t *testing.T) {
					f := newFixture(t)
					nativeProvider(f, t, "anthropic")
					stop := `null`
					if reason == "stop_sequence" {
						stop = `"END"`
					}
					f.upstream.stream(`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"partial or complete text"}}`, `{"type":"message_delta","delta":{"stop_reason":"`+reason+`","stop_sequence":`+stop+`},"usage":{"output_tokens":5}}`, `{"type":"message_stop"}`)
					w := f.do(t, http.MethodPost, endpoint, f.token, protocolBody(f.model.ID, endpoint, stream))
					raw := w.Body.String()
					if reason == "pause_turn" && endpoint != "/v1/messages" {
						if !stream && w.Code != 502 {
							t.Fatalf("unsupported pause silently completed: %d %s", w.Code, raw)
						}
						if stream && (strings.Contains(raw, `"finish_reason":"stop"`) || strings.Contains(raw, "event: response.completed")) {
							t.Fatal(raw)
						}
					} else {
						if w.Code != 200 {
							t.Fatalf("status=%d body=%s", w.Code, raw)
						}
						if endpoint == "/v1/messages" && !strings.Contains(raw, `"stop_reason":"`+reason+`"`) {
							t.Fatal(raw)
						}
						if endpoint == "/v1/messages" && reason == "stop_sequence" && !strings.Contains(raw, `"stop_sequence":"END"`) {
							t.Fatal(raw)
						}
						if reason == "max_tokens" || reason == "model_context_window_exceeded" {
							if endpoint == "/v1/chat/completions" && !strings.Contains(raw, `"finish_reason":"length"`) {
								t.Fatal(raw)
							}
							if endpoint == "/v1/responses" && !strings.Contains(raw, `"status":"incomplete"`) {
								t.Fatal(raw)
							}
						}
					}
					if records := f.turns(); len(records) != 1 || records[0].Usage.Total() != 15 {
						t.Fatalf("records=%+v", records)
					}
				})
			}
		}
	}
}

func TestToolResultErrorFlagSurvivesBothUpstreamProtocols(t *testing.T) {
	for _, kind := range []string{"openai", "anthropic"} {
		f := newFixture(t)
		nativeProvider(f, t, kind)
		if kind == "openai" {
			f.upstream.reply(answer)
		} else {
			f.upstream.reply(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2}}`)
		}
		body := `{"model":"` + f.model.ID + `","messages":[{"role":"user","content":"run"},{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"run","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","is_error":true,"content":"failed"}]}]}`
		w := f.do(t, http.MethodPost, "/v1/messages", f.token, body)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		raw := f.upstream.receivedRaw()
		if kind == "anthropic" && !strings.Contains(raw, `"is_error":true`) {
			t.Fatalf("native error flag dropped: %s", raw)
		}
		if kind == "openai" && !strings.Contains(raw, `[tool_error]\nfailed`) {
			t.Fatalf("cross-protocol error flag dropped: %s", raw)
		}
	}
}

func TestToolAndSystemCacheBreakpointsSurviveBothUpstreamProtocols(t *testing.T) {
	for _, kind := range []string{"openai", "anthropic"} {
		for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
			f := newFixture(t)
			nativeProvider(f, t, kind)
			if kind == "openai" {
				f.upstream.reply(answer)
			} else {
				f.upstream.reply(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2}}`)
			}
			var body string
			cache := `{"type":"ephemeral","ttl":"1h"}`
			switch endpoint {
			case "/v1/chat/completions":
				body = `{"model":"` + f.model.ID + `","messages":[{"role":"system","content":[{"type":"text","text":"prefix","cache_control":` + cache + `}]},{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"run","parameters":{"type":"object"}},"cache_control":` + cache + `}]}`
			case "/v1/responses":
				body = `{"model":"` + f.model.ID + `","input":[{"role":"system","content":[{"type":"input_text","text":"prefix","cache_control":` + cache + `}]},{"role":"user","content":"hi"}],"tools":[{"type":"function","name":"run","parameters":{"type":"object"},"cache_control":` + cache + `}]}`
			case "/v1/messages":
				body = `{"model":"` + f.model.ID + `","system":[{"type":"text","text":"prefix","cache_control":` + cache + `}],"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"run","input_schema":{"type":"object"},"cache_control":` + cache + `}]}`
			}
			w := f.do(t, http.MethodPost, endpoint, f.token, body)
			if w.Code != 200 {
				t.Fatalf("%s/%s: %d %s", kind, endpoint, w.Code, w.Body.String())
			}
			if count := strings.Count(f.upstream.receivedRaw(), `"cache_control"`); count != 2 {
				t.Fatalf("%s/%s breakpoints=%d: %s", kind, endpoint, count, f.upstream.receivedRaw())
			}
		}
	}
}

func TestForcedHostedToolsAndNestedResultBreakpointsFailBeforeBilling(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		f := newFixture(t)
		var body string
		switch endpoint {
		case "/v1/chat/completions":
			body = `{"model":"` + f.model.ID + `","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"web_search"}],"tool_choice":"required"}`
		case "/v1/responses":
			body = `{"model":"` + f.model.ID + `","input":"hi","tools":[{"type":"web_search"}],"tool_choice":"required"}`
		case "/v1/messages":
			body = `{"model":"` + f.model.ID + `","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"web_search","type":"web_search_20250305"}],"tool_choice":{"type":"any"}}`
		}
		w := f.do(t, http.MethodPost, endpoint, f.token, body)
		if w.Code != 400 || len(f.turns()) != 0 || len(f.upstream.received()) != 0 {
			t.Fatalf("%s status=%d records=%+v body=%s", endpoint, w.Code, f.turns(), w.Body.String())
		}
	}
	f := newFixture(t)
	body := `{"model":"` + f.model.ID + `","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":[{"type":"text","text":"result","cache_control":{"type":"ephemeral"}}]}]}]}`
	w := f.do(t, http.MethodPost, "/v1/messages", f.token, body)
	if w.Code != 400 || len(f.turns()) != 0 {
		t.Fatalf("nested breakpoint silently flattened: %d %s", w.Code, w.Body.String())
	}
}

func TestAccountingWeightsAndCacheCountsAreAppliedOnce(t *testing.T) {
	f := newFixture(t)
	req, in, out, reason := 5.0, 1.0, 2.0, 3.0
	if _, err := f.models.Update(context.Background(), f.model.ID, model.Update{RequestWeight: &req, InputTokenWeight: &in, OutputTokenWeight: &out, ReasoningTokenWeight: &reason}); err != nil {
		t.Fatal(err)
	}
	f.upstream.stream(`{"choices":[{"delta":{"content":"ok"}}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":40,"total_tokens":140,"completion_tokens_details":{"reasoning_tokens":30}}}`)
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, protocolBody(f.model.ID, "/v1/chat/completions", false))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	nativeProvider(f, t, "anthropic")
	f.upstream.stream(`{"type":"message_start","message":{"usage":{"input_tokens":0,"cache_read_input_tokens":900,"cache_creation_input_tokens":100,"output_tokens":1}}}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`, `{"type":"message_stop"}`)
	w = f.do(t, http.MethodPost, "/v1/messages", f.token, protocolBody(f.model.ID, "/v1/messages", false))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	records := f.turns()
	if len(records) != 2 || records[0].Usage.Total() != 140 || records[1].Usage.Total() != 1005 || math.Abs(records[0].Credits-5.21) > 1e-10 || math.Abs(records[1].Credits-6.01) > 1e-10 {
		t.Fatalf("records=%+v", records)
	}
	if records[0].Status != chat.StatusOK || records[1].Status != chat.StatusOK {
		t.Fatal(records)
	}
}

func TestResponsesParallelReplayWorksWithAnthropicUpstream(t *testing.T) {
	f := newFixture(t)
	nativeProvider(f, t, "anthropic")
	f.upstream.reply(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2}}`)
	body := `{"model":"` + f.model.ID + `","input":[{"role":"user","content":"hi"},{"type":"function_call","call_id":"a","name":"one","arguments":"{}"},{"type":"function_call","call_id":"b","name":"two","arguments":"{}"},{"type":"function_call_output","call_id":"a","output":"one"},{"type":"function_call_output","call_id":"b","output":"two"}]}`
	w := f.do(t, http.MethodPost, "/v1/responses", f.token, body)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	raw := f.upstream.receivedRaw()
	var sent struct {
		Messages []struct {
			Role    string
			Content []struct{ Type, ID, ToolUseID string }
		}
	}
	if err := json.Unmarshal([]byte(raw), &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent.Messages) != 3 || len(sent.Messages[1].Content) != 2 || len(sent.Messages[2].Content) != 2 {
		t.Fatalf("parallel turn not reconstructed: %s", raw)
	}
}

func TestExplicitZeroUsageIsReturnedWithoutInventedTokenCharges(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		for _, stream := range []bool{false, true} {
			f := newFixture(t)
			f.upstream.stream(`{"choices":[{"delta":{"content":"cached response"}}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`)
			body := protocolBody(f.model.ID, endpoint, stream)
			if endpoint == "/v1/chat/completions" && stream {
				body = strings.TrimSuffix(body, "}") + `,"stream_options":{"include_usage":true}}`
			}
			w := f.do(t, http.MethodPost, endpoint, f.token, body)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"total_tokens":0`) {
				t.Fatalf("%s/%v: %d %s", endpoint, stream, w.Code, w.Body.String())
			}
			records := f.turns()
			if len(records) != 1 || records[0].Usage.Total() != 0 || records[0].Usage.Estimated || records[0].Credits != 0 {
				t.Fatalf("records=%+v", records)
			}
		}
	}
}

func TestCanonicalUsageShapesOnEverySurface(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		for _, stream := range []bool{false, true} {
			f := newFixture(t)
			f.upstream.stream(`{"choices":[{"delta":{"content":"ok"}}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":40,"total_tokens":140,"prompt_tokens_details":{"cached_tokens":70,"cache_write_tokens":20},"completion_tokens_details":{"reasoning_tokens":30}}}`)
			body := protocolBody(f.model.ID, endpoint, stream)
			if endpoint == "/v1/chat/completions" && stream {
				body = strings.TrimSuffix(body, "}") + `,"stream_options":{"include_usage":true}}`
			}
			w := f.do(t, http.MethodPost, endpoint, f.token, body)
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			var usage map[string]any
			decode := func(raw string) {
				var object map[string]any
				_ = json.Unmarshal([]byte(raw), &object)
				if response, ok := object["response"].(map[string]any); ok {
					object = response
				}
				if value, ok := object["usage"].(map[string]any); ok {
					usage = value
				}
			}
			if stream {
				for _, line := range strings.Split(w.Body.String(), "\n") {
					if strings.HasPrefix(line, "data: {") {
						decode(strings.TrimPrefix(line, "data: "))
					}
				}
			} else {
				decode(w.Body.String())
			}
			switch endpoint {
			case "/v1/chat/completions":
				if usage["prompt_tokens"] != float64(100) || usage["completion_tokens"] != float64(40) || usage["total_tokens"] != float64(140) {
					t.Fatal(usage)
				}
				if usage["prompt_tokens_details"].(map[string]any)["cached_tokens"] != float64(70) || usage["completion_tokens_details"].(map[string]any)["reasoning_tokens"] != float64(30) {
					t.Fatal(usage)
				}
			case "/v1/responses":
				if usage["input_tokens"] != float64(100) || usage["output_tokens"] != float64(40) || usage["total_tokens"] != float64(140) || usage["input_tokens_details"].(map[string]any)["cache_write_tokens"] != float64(20) || usage["output_tokens_details"].(map[string]any)["reasoning_tokens"] != float64(30) {
					t.Fatal(usage)
				}
			case "/v1/messages":
				if usage["input_tokens"] != float64(10) || usage["cache_read_input_tokens"] != float64(70) || usage["cache_creation_input_tokens"] != float64(20) || usage["output_tokens"] != float64(40) {
					t.Fatal(usage)
				}
			}
			if records := f.turns(); len(records) != 1 || records[0].Usage.Total() != 140 {
				t.Fatal(records)
			}
		}
	}
}

func TestAnthropicStructuredOutputStopAndXhighControls(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		f := newFixture(t)
		nativeProvider(f, t, "anthropic")
		f.upstream.reply(`{"content":[{"type":"text","text":"{}"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2}}`)
		body := protocolBody(f.model.ID, endpoint, false)
		schema := `{"type":"object","properties":{},"additionalProperties":false}`
		switch endpoint {
		case "/v1/chat/completions":
			body = strings.TrimSuffix(body, "}") + `,"stop":["END"],"reasoning_effort":"xhigh","response_format":{"type":"json_schema","json_schema":{"name":"result","schema":` + schema + `}},"cache_control":{"type":"ephemeral"}}`
		case "/v1/responses":
			body = strings.TrimSuffix(body, "}") + `,"parallel_tool_calls":false,"text":{"format":{"type":"json_schema","name":"result","schema":` + schema + `}},"cache_control":{"type":"ephemeral"}}`
		case "/v1/messages":
			body = strings.TrimSuffix(body, "}") + `,"stop_sequences":["END"],"output_config":{"format":{"type":"json_schema","schema":` + schema + `}}}`
		}
		w := f.do(t, http.MethodPost, endpoint, f.token, body)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", endpoint, w.Code, w.Body.String())
		}
		sent := f.upstream.received()
		if sent["output_config"].(map[string]any)["format"].(map[string]any)["type"] != "json_schema" {
			t.Fatal(sent)
		}
		if endpoint != "/v1/responses" && sent["stop_sequences"] == nil {
			t.Fatal(sent)
		}
		if endpoint != "/v1/messages" && sent["cache_control"] == nil {
			t.Fatal(sent)
		}
		if endpoint == "/v1/responses" && decodeJSON(t, w)["parallel_tool_calls"] != false {
			t.Fatal(w.Body.String())
		}
	}
	f := newFixture(t)
	f.upstream.reply(answer)
	body := `{"model":"` + f.model.ID + `","reasoning_effort":"xhigh","messages":[{"role":"user","content":"hi"}]}`
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
	if w.Code != 200 || f.upstream.received()["reasoning_effort"] != "xhigh" {
		t.Fatal(f.upstream.received())
	}
}

func TestReasoningOnlyNonStreamingModelsKeepBufferedReasoning(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		f := newFixture(t)
		off := false
		if _, err := f.models.Update(context.Background(), f.model.ID, model.Update{SupportsStreaming: &off}); err != nil {
			t.Fatal(err)
		}
		f.upstream.reply(`{"choices":[{"message":{"content":"","reasoning_content":"Only reasoning."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30,"completion_tokens_details":{"reasoning_tokens":20}}}`)
		w := f.do(t, http.MethodPost, endpoint, f.token, protocolBody(f.model.ID, endpoint, false))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Only reasoning.") {
			t.Fatalf("%s: %d %s", endpoint, w.Code, w.Body.String())
		}
		if records := f.turns(); len(records) != 1 || records[0].Usage.Total() != 30 || records[0].Usage.OutputTokens != 0 || records[0].Usage.ReasoningTokens != 20 {
			t.Fatal(records)
		}
	}
}

func TestGW01AuditAccountingExample(t *testing.T) {
	f := newFixture(t)
	one := 1.0
	if _, err := f.models.Update(context.Background(), f.model.ID, model.Update{ReasoningTokenWeight: &one}); err != nil {
		t.Fatal(err)
	}
	f.upstream.reply(`{"choices":[{"message":{"content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":1000,"total_tokens":1100,"completion_tokens_details":{"reasoning_tokens":800}}}`)
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, protocolBody(f.model.ID, "/v1/chat/completions", false))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	records := f.turns()
	if len(records) != 1 || records[0].Usage.InputTokens != 100 || records[0].Usage.OutputTokens != 200 || records[0].Usage.ReasoningTokens != 800 || records[0].Usage.Total() != 1100 || math.Abs(records[0].Credits-1.1) > 1e-10 {
		t.Fatalf("audit case remains double counted: %+v", records)
	}
	usage := decodeJSON(t, w)["usage"].(map[string]any)
	if usage["completion_tokens"] != float64(1000) || usage["total_tokens"] != float64(1100) {
		t.Fatal(usage)
	}
}

func TestGW09MessagesCumulativeUsageMatchesBuffered(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(`{"choices":[{"message":{"content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":40,"completion_tokens":9}}`)
	buffered := f.do(t, http.MethodPost, "/v1/messages", f.token, protocolBody(f.model.ID, "/v1/messages", false))
	want := decodeJSON(t, buffered)["usage"].(map[string]any)
	streamed := f.do(t, http.MethodPost, "/v1/messages", f.token, protocolBody(f.model.ID, "/v1/messages", true))
	snapshot := map[string]any{}
	for _, line := range strings.Split(streamed.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event["type"] == "message_start" {
			for k, v := range event["message"].(map[string]any)["usage"].(map[string]any) {
				snapshot[k] = v
			}
		}
		if event["type"] == "message_delta" {
			for k, v := range event["usage"].(map[string]any) {
				snapshot[k] = v
			}
		}
	}
	if snapshot["input_tokens"] != float64(40) || snapshot["output_tokens"] != float64(9) || snapshot["input_tokens"] != want["input_tokens"] || snapshot["output_tokens"] != want["output_tokens"] {
		t.Fatalf("buffered=%v streamed=%v", want, snapshot)
	}
	if records := f.turns(); len(records) != 2 || records[0].Usage.Total() != 49 || records[1].Usage.Total() != 49 {
		t.Fatal(records)
	}
}

func TestReasoningOnlyMessagesOutputFailsExplicitlyInsteadOfEmptySuccess(t *testing.T) {
	for _, stream := range []bool{false, true} {
		f := newFixture(t)
		off := false
		if _, err := f.models.Update(context.Background(), f.model.ID, model.Update{SupportsStreaming: &off}); err != nil {
			t.Fatal(err)
		}
		f.upstream.reply(`{"choices":[{"message":{"content":"","reasoning_content":"Only reasoning."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30,"completion_tokens_details":{"reasoning_tokens":20}}}`)
		w := f.do(t, http.MethodPost, "/v1/messages", f.token, protocolBody(f.model.ID, "/v1/messages", stream))
		if !stream && w.Code != 502 {
			t.Fatalf("reasoning-only output silently succeeded: %d %s", w.Code, w.Body.String())
		}
		if stream && (!strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "event: message_stop")) {
			t.Fatal(w.Body.String())
		}
		if records := f.turns(); len(records) != 1 || records[0].Usage.Total() != 30 || records[0].Status != chat.StatusError {
			t.Fatal(records)
		}
	}
}
