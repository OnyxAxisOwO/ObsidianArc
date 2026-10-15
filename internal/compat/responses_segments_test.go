package compat

import (
	"net/http"
	"testing"
)

// Text, thinking and more text are three items. Each finishes with only its own
// words, under its own id: a segment that repeated the one before it, or
// reused its id, would show the client a duplicated answer.
func TestResponsesStreamedSegmentsEachFinishWithTheirOwnText(t *testing.T) {
	f := newFixture(t)
	f.upstream.stream(
		`{"choices":[{"delta":{"role":"assistant","content":"Alpha. "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"Beta."}}]}`,
		`{"choices":[{"delta":{"content":"Gamma."}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`,
	)

	w := f.do(t, http.MethodPost, "/v1/responses", f.token,
		`{"model":"`+f.model.ID+`","stream":true,"store":false,"input":"what is it?"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	raw := w.Body.String()

	texts := streamedFrames(t, raw, "response.output_text.done")
	if len(texts) != 2 {
		t.Fatalf("output_text.done events = %d, want 2:\n%s", len(texts), raw)
	}
	if texts[0]["text"] != "Alpha. " || texts[1]["text"] != "Gamma." {
		t.Errorf("segments finished with %q and %q, want their own words:\n%s",
			texts[0]["text"], texts[1]["text"], raw)
	}
	if texts[0]["item_id"] == texts[1]["item_id"] {
		t.Errorf("two text items share the id %v:\n%s", texts[0]["item_id"], raw)
	}

	completed := streamedFrames(t, raw, "response.completed")
	if len(completed) != 1 {
		t.Fatalf("response.completed events = %d:\n%s", len(completed), raw)
	}
	response, _ := completed[0]["response"].(map[string]any)
	if output, _ := response["output"].([]any); len(output) != 3 {
		t.Errorf("response.completed lists %d items, want the three that streamed:\n%s", len(output), raw)
	}
}
