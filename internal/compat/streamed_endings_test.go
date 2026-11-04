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
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

// departingClient is a connection that takes the start of a stream and then
// refuses every write, as a socket does once the browser has gone. The provider
// call ends with the write's error, not with a cancelled request context.
type departingClient struct {
	*httptest.ResponseRecorder
	gone bool
}

func (c *departingClient) Write(p []byte) (int, error) {
	if !c.gone && bytes.Contains(p, []byte("second")) {
		c.gone = true
	}
	if c.gone {
		return 0, errors.New("write: broken pipe")
	}
	return c.ResponseRecorder.Write(p)
}

// A client that goes away mid-answer is an abort in the ledger. The write that
// failed is what ended the turn, so recording it as an error would blame the
// provider and this server for a hang-up neither caused.
func TestAClientThatDepartsMidStreamIsRecordedAsAborted(t *testing.T) {
	f := newFixture(t)
	f.upstream.stream(
		`{"choices":[{"delta":{"role":"assistant","content":"first "}}]}`,
		`{"choices":[{"delta":{"content":"second "}}]}`,
		`{"choices":[{"delta":{"content":"third"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`,
	)

	body := `{"model":"` + f.model.ID + `","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.token)
	f.mux.ServeHTTP(&departingClient{ResponseRecorder: httptest.NewRecorder()}, r)

	records := f.turns()
	if len(records) != 1 {
		t.Fatalf("recorded %d turns, want 1", len(records))
	}
	if records[0].Status != chat.StatusAborted {
		t.Errorf("status = %q, want %q: a hang-up is not a failure", records[0].Status, chat.StatusAborted)
	}
}

// A model that does not stream still thinks before it answers. A streaming
// client is shown that thinking ahead of the text, as it would be from a model
// that streams; this surface used to send only the text.
func TestThinkingReachesAStreamingClientFromANonStreamingModel(t *testing.T) {
	f := newFixture(t)
	off := false
	if _, err := f.models.Update(context.Background(), f.model.ID,
		model.Update{SupportsStreaming: &off}); err != nil {
		t.Fatal(err)
	}
	f.upstream.reply(`{"choices":[{"message":{"content":"42.","reasoning_content":"Count the ways."},` +
		`"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`)

	body := `{"model":"` + f.model.ID + `","stream":true,"messages":[{"role":"user","content":"what is the answer?"}]}`
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	raw := w.Body.String()
	thinking := strings.Index(raw, `"reasoning_content":"Count the ways."`)
	text := strings.Index(raw, `"content":"42."`)
	if thinking < 0 {
		t.Fatalf("the thinking never reached the client: %s", raw)
	}
	if text < 0 || thinking > text {
		t.Errorf("the thinking did not come before the answer: %s", raw)
	}
}

// Thinking that arrives after visible text is an item of its own, and the text
// closes first. Each item must be finished at the output_index it was added at;
// when the thinking opened while the text was still open, both claimed index 0.
func TestResponsesThinkingAfterTextKeepsEachItemsIndex(t *testing.T) {
	f := newFixture(t)
	f.upstream.stream(
		`{"choices":[{"delta":{"role":"assistant","content":"Answer one "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"why"}}]}`,
		`{"choices":[{"delta":{"content":"two"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`,
	)

	w := f.do(t, http.MethodPost, "/v1/responses", f.token,
		`{"model":"`+f.model.ID+`","stream":true,"store":false,"input":"hi"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	raw := w.Body.String()

	added := map[string]float64{}
	for _, event := range streamedFrames(t, raw, "response.output_item.added") {
		item, _ := event["item"].(map[string]any)
		id, _ := item["id"].(string)
		index, _ := event["output_index"].(float64)
		added[id] = index
	}
	if len(added) != 3 {
		t.Fatalf("items added = %d, want text, thinking and text again:\n%s", len(added), raw)
	}
	done := streamedFrames(t, raw, "response.output_item.done")
	if len(done) != 3 {
		t.Fatalf("items done = %d, want 3:\n%s", len(done), raw)
	}
	for _, event := range done {
		item, _ := event["item"].(map[string]any)
		id, _ := item["id"].(string)
		index, _ := event["output_index"].(float64)
		if want, ok := added[id]; !ok || want != index {
			t.Errorf("item %s finished at output_index %v, but was added at %v:\n%s", id, index, want, raw)
		}
	}
}

// streamedFrames decodes every event of one name from a raw event stream.
func streamedFrames(t *testing.T, raw, name string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, block := range strings.Split(raw, "\n\n") {
		lines := strings.Split(block, "\n")
		if len(lines) < 2 || lines[0] != "event: "+name {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &payload); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		out = append(out, payload)
	}
	return out
}
