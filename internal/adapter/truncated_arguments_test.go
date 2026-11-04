package adapter

import (
	"context"
	"errors"
	"testing"
)

// A call the model was cut off in the middle of is not handed on, on any path.
// Its tool would run on arguments the model never finished, which is worse than
// ending the turn with a reason.
func TestTruncatedToolArgumentsAreRefusedOnEveryPath(t *testing.T) {
	chat := func(t *testing.T, provider Provider, stream bool, out *collected) error {
		t.Helper()
		_, err := testRegistry().Chat(context.Background(), provider,
			ChatRequest{Model: testModel(), Stream: stream, Tools: toolFixture(),
				Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
			out.sink)
		return err
	}
	requireUpstream := func(t *testing.T, err error, out *collected) {
		t.Helper()
		var upstream *Error
		if !errors.As(err, &upstream) || upstream.Kind != ErrorUpstream {
			t.Fatalf("err = %v, want an upstream error for the cut-off arguments", err)
		}
		if len(out.calls) != 0 {
			t.Errorf("a call with cut-off arguments was delivered: %+v", out.calls)
		}
	}

	t.Run("buffered openai", func(t *testing.T) {
		server := jsonServer(t,
			`{"choices":[{"message":{"content":null,"tool_calls":[{"id":"c","type":"function",`+
				`"function":{"name":"lookup","arguments":"{\"path\":\"ma"}}]},"finish_reason":"length"}],`+
				`"usage":{"prompt_tokens":1,"completion_tokens":1}}`, nil)
		var out collected
		err := chat(t, Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"}, false, &out)
		requireUpstream(t, err, &out)
	})

	t.Run("streamed anthropic", func(t *testing.T) {
		server := sseServer(t, []string{
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t","name":"lookup","input":{}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"ma"}}`,
			`{"type":"content_block_stop","index":0}`,
		}, nil)
		var out collected
		err := chat(t, Provider{Kind: KindAnthropic, BaseURL: server.URL, APIKey: "k"}, true, &out)
		requireUpstream(t, err, &out)
	})
}
