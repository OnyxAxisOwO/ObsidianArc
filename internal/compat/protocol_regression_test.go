package compat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

func protocolBody(model, endpoint string, stream bool) string {
	body := map[string]any{"model": model, "stream": stream}
	if endpoint == "/v1/responses" {
		body["input"] = "hi"
		body["store"] = false
	} else {
		body["messages"] = []any{map[string]any{"role": "user", "content": "hi"}}
	}
	encoded, _ := json.Marshal(body)
	return string(encoded)
}

func TestDamagedUpstreamFailsEverySurfaceAndRecordsOnce(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		for _, stream := range []bool{false, true} {
			for _, malformed := range []bool{false, true} {
				t.Run(endpoint+strings.Repeat("stream", boolInt(stream))+strings.Repeat("malformed", boolInt(malformed)), func(t *testing.T) {
					f := newFixture(t)
					frames := []string{`{"choices":[{"delta":{"content":"partial answer"}}]}`}
					if malformed {
						frames = append(frames, `{bad`, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
					}
					f.upstream.stream(frames...)
					w := f.do(t, http.MethodPost, endpoint, f.token, protocolBody(f.model.ID, endpoint, stream))
					if !stream && w.Code != http.StatusBadGateway {
						t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
					}
					if stream && (strings.Contains(w.Body.String(), `"finish_reason":"stop"`) || strings.Contains(w.Body.String(), "event: message_stop") || strings.Contains(w.Body.String(), "event: response.completed")) {
						t.Fatalf("damaged stream completed successfully: %s", w.Body.String())
					}
					records := f.turns()
					if len(records) != 1 || records[0].Status != chat.StatusError || records[0].Usage.Total() == 0 || !records[0].Usage.Estimated {
						t.Fatalf("records=%+v", records)
					}
				})
			}
		}
	}
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestChatStreamingUsageIsOptInAndUsesEmptyChoices(t *testing.T) {
	for _, include := range []bool{false, true} {
		f := newFixture(t)
		f.upstream.stream(`{"choices":[{"delta":{"content":"ok"}}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":70},"completion_tokens_details":{"reasoning_tokens":30}}}`)
		body := protocolBody(f.model.ID, "/v1/chat/completions", true)
		if include {
			body = strings.TrimSuffix(body, "}") + `,"stream_options":{"include_usage":true}}`
		}
		w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
		var usages int
		for _, line := range strings.Split(w.Body.String(), "\n") {
			if !strings.HasPrefix(line, "data: {") {
				continue
			}
			var event map[string]json.RawMessage
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
				t.Fatal(line)
			}
			if raw, ok := event["usage"]; ok {
				usages++
				if string(event["choices"]) != "[]" {
					t.Fatalf("usage chunk has choices: %s", line)
				}
				var usage map[string]any
				_ = json.Unmarshal(raw, &usage)
				if usage["total_tokens"] != float64(140) || usage["completion_tokens"] != float64(40) {
					t.Fatalf("usage=%s", raw)
				}
			}
		}
		if usages != boolInt(include) {
			t.Fatalf("include=%v usage chunks=%d: %s", include, usages, w.Body.String())
		}
		records := f.turns()
		if len(records) != 1 || records[0].Usage.Total() != 140 || records[0].Usage.OutputTokens != 10 || records[0].Usage.ReasoningTokens != 30 {
			t.Fatalf("ledger=%+v", records)
		}
	}
}

func TestPromptAndOutputControlsReachUpstream(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		f := newFixture(t)
		f.upstream.reply(answer)
		body := protocolBody(f.model.ID, endpoint, false)
		extra := `,"prompt_cache_key":"stable","prompt_cache_retention":"24h","parallel_tool_calls":false`
		schema := `{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`
		switch endpoint {
		case "/v1/chat/completions":
			extra += `,"stop":"END","response_format":{"type":"json_schema","json_schema":{"name":"result","strict":true,"schema":` + schema + `}}`
		case "/v1/responses":
			extra += `,"text":{"format":{"type":"json_schema","name":"result","strict":true,"schema":` + schema + `}}`
		case "/v1/messages":
			extra = `,"stop_sequences":["END"],"system":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral","ttl":"1h"}}],"output_config":{"format":{"type":"json_schema","schema":` + schema + `}}`
		}
		body = strings.TrimSuffix(body, "}") + extra + "}"
		w := f.do(t, http.MethodPost, endpoint, f.token, body)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", endpoint, w.Code, w.Body.String())
		}
		sent := f.upstream.received()
		if sent["response_format"] == nil {
			t.Fatalf("%s: missing output format: %+v", endpoint, sent)
		}
		if endpoint != "/v1/messages" && (sent["prompt_cache_key"] != "stable" || sent["prompt_cache_retention"] != "24h" || sent["parallel_tool_calls"] != false) {
			t.Fatalf("%s: %+v", endpoint, sent)
		}
		if endpoint != "/v1/responses" && sent["stop"] == nil {
			t.Fatalf("%s: stop dropped", endpoint)
		}
		if endpoint == "/v1/messages" && !strings.Contains(f.upstream.receivedRaw(), `"cache_control"`) {
			t.Fatal("system cache breakpoint dropped")
		}
	}
}

func TestMessagesNativeCacheUsageStartsWithRealInput(t *testing.T) {
	f := newFixture(t)
	if _, err := f.db.Exec(context.Background(), `UPDATE providers SET kind = ? WHERE id = ?`, "anthropic", f.model.ProviderID); err != nil {
		t.Fatal(err)
	}
	f.upstream.stream(
		`{"type":"message_start","message":{"usage":{"input_tokens":0,"cache_read_input_tokens":900,"cache_creation_input_tokens":100,"output_tokens":1}}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"stop_sequence","stop_sequence":"END"},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	)
	w := f.do(t, http.MethodPost, "/v1/messages", f.token, protocolBody(f.model.ID, "/v1/messages", true))
	raw := w.Body.String()
	start := strings.Split(strings.Split(raw, "event: message_start\n")[1], "\n\n")[0]
	for _, expected := range []string{`"input_tokens":0`, `"cache_read_input_tokens":900`, `"cache_creation_input_tokens":100`} {
		if !strings.Contains(start, expected) {
			t.Fatalf("message_start missing %s: %s", expected, start)
		}
	}
	if !strings.Contains(raw, `"stop_sequence":"END"`) {
		t.Fatal(raw)
	}
	records := f.turns()
	if len(records) != 1 || records[0].Usage.InputTokens != 1000 || records[0].Usage.Total() != 1005 || records[0].Usage.Estimated {
		t.Fatalf("ledger=%+v", records)
	}
}

func TestResponsesParallelReplayAndAmbiguousNames(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)
	body := `{"model":"` + f.model.ID + `","input":[{"role":"user","content":"hi"},{"type":"function_call","call_id":"a","name":"one","arguments":"{}"},{"type":"function_call","call_id":"b","name":"two","arguments":"{}"},{"type":"function_call_output","call_id":"a","output":"one"},{"type":"function_call_output","call_id":"b","output":"two"}]}`
	w := f.do(t, http.MethodPost, "/v1/responses", f.token, body)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	sent := f.upstream.received()["messages"].([]any)
	assistant := sent[1].(map[string]any)
	if calls := assistant["tool_calls"].([]any); len(calls) != 2 {
		t.Fatalf("parallel calls split: %+v", sent)
	}
	body = `{"model":"` + f.model.ID + `","input":"hi","tools":[{"type":"namespace","name":"first","tools":[{"type":"function","name":"run"}]},{"type":"namespace","name":"second","tools":[{"type":"function","name":"run"}]}]}`
	w = f.do(t, http.MethodPost, "/v1/responses", f.token, body)
	if w.Code != 400 {
		t.Fatalf("ambiguous names silently merged: %d %s", w.Code, w.Body.String())
	}
}

type flushOnlyDeparture struct {
	*httptest.ResponseRecorder
	fail    bool
	closing bool
}

func (w *flushOnlyDeparture) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("second")) || (w.closing && bytes.Contains(p, []byte(`"finish_reason":"stop"`))) {
		w.fail = true
	}
	return w.ResponseRecorder.Write(p)
}
func (w *flushOnlyDeparture) FlushError() error {
	if w.fail {
		return errors.New("flush: broken pipe")
	}
	w.ResponseRecorder.Flush()
	return nil
}

func TestWrappedFlushFailureAbortsAndRecordsOnce(t *testing.T) {
	for _, closing := range []bool{false, true} {
		f := newFixture(t)
		f.upstream.stream(`{"choices":[{"delta":{"content":"first"}}]}`, `{"choices":[{"delta":{"content":"second"}}]}`, `{"choices":[{"delta":{"content":"third"}}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`)
		writer := &flushOnlyDeparture{ResponseRecorder: httptest.NewRecorder(), closing: closing}
		if closing {
			f.upstream.stream(`{"choices":[{"delta":{"content":"first"}}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`)
		}
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(protocolBody(f.model.ID, "/v1/chat/completions", true)))
		r.Header.Set("Authorization", "Bearer "+f.token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept-Encoding", "gzip")
		httpx.Chain(f.mux, httpx.RequestID(), httpx.Logger(), httpx.Recover(), httpx.Compress()).ServeHTTP(writer, r)
		records := f.turns()
		if len(records) != 1 || records[0].Status != chat.StatusAborted {
			t.Fatalf("closing=%v records=%+v", closing, records)
		}
		if !closing && strings.Contains(writer.Body.String(), "third") {
			t.Fatalf("upstream continued after flush failure: %s", writer.Body.String())
		}
	}
}

func TestUnsupportedControlsAreRefusedBeforeCallingOrBilling(t *testing.T) {
	f := newFixture(t)
	if _, err := f.db.Exec(context.Background(), `UPDATE providers SET kind = ? WHERE id = ?`, "anthropic", f.model.ProviderID); err != nil {
		t.Fatal(err)
	}
	body := `{"model":"` + f.model.ID + `","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
	if w.Code != http.StatusBadRequest || len(f.turns()) != 0 || len(f.upstream.received()) != 0 {
		t.Fatalf("status=%d records=%+v upstream=%+v", w.Code, f.turns(), f.upstream.received())
	}
}
