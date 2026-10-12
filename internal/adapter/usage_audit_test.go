package adapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A refusal or an empty answer that arrived as a complete JSON document still
// carries the usage the provider billed for the prompt and whatever it wrote.
// Dropping it on the error path made the ledger record only the request fee.
func TestAnAnswerThatFailsWithUsageKeepsTheUsage(t *testing.T) {
	cases := []struct {
		name     string
		kind     Kind
		body     string
		refusal  bool
		streamed bool
		output   int
	}{
		{
			name:    "anthropic refusal",
			kind:    KindAnthropic,
			body:    `{"content":[],"stop_reason":"refusal","usage":{"input_tokens":2000,"output_tokens":7}}`,
			refusal: true,
			output:  7,
		},
		{
			name:     "anthropic refusal answered to a streaming request",
			kind:     KindAnthropic,
			body:     `{"content":[],"stop_reason":"refusal","usage":{"input_tokens":2000,"output_tokens":7}}`,
			refusal:  true,
			streamed: true,
			output:   7,
		},
		{
			name:    "openai refusal",
			kind:    KindOpenAI,
			body:    `{"choices":[{"message":{"content":null,"refusal":"no"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2000,"completion_tokens":7}}`,
			refusal: true,
			output:  7,
		},
		{
			name: "openai no choices",
			kind: KindOpenAI,
			body: `{"choices":[],"usage":{"prompt_tokens":2000,"completion_tokens":0}}`,
		},
		{
			name:     "openai refusal answered to a streaming request",
			kind:     KindOpenAI,
			body:     `{"choices":[{"message":{"content":null,"refusal":"no"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2000,"completion_tokens":7}}`,
			refusal:  true,
			streamed: true,
			output:   7,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			}))
			t.Cleanup(server.Close)

			var out collected
			result, err := testRegistry().Chat(context.Background(),
				Provider{Kind: tc.kind, BaseURL: server.URL, APIKey: "k"},
				ChatRequest{Model: testModel(), Stream: tc.streamed, Messages: []Message{
					{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}},
				}},
				out.sink)
			if err == nil {
				t.Fatal("the failure was not reported")
			}
			var adapterErr *Error
			if tc.refusal && (!errors.As(err, &adapterErr) || adapterErr.Kind != ErrorRefusal) {
				t.Fatalf("error = %v, want a refusal", err)
			}
			if result.Usage.InputTokens != 2000 {
				t.Errorf("input = %d, want the billed 2000 kept", result.Usage.InputTokens)
			}
			if result.Usage.OutputTokens+result.Usage.ReasoningTokens != tc.output {
				t.Errorf("output = %+v, want the billed output kept", result.Usage)
			}
		})
	}
}
