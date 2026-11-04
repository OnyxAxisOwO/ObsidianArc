package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A provider decides how much it sends. These hold the ceiling that stops one
// from deciding on more than this process can hold.

func withResponseLimit(t *testing.T, limit int64) {
	t.Helper()
	previous := responseLimit
	responseLimit = limit
	t.Cleanup(func() { responseLimit = previous })
}

func isTooLarge(err error) bool {
	var adapterErr *Error
	return errors.As(err, &adapterErr) && adapterErr.Kind == ErrorUpstream &&
		errors.Is(err, errResponseTooLarge)
}

func TestLimitedReaderFailsInsteadOfEndingQuietly(t *testing.T) {
	exact, err := io.ReadAll(newLimitedReader(strings.NewReader(strings.Repeat("a", 100)), 100))
	if err != nil || len(exact) != 100 {
		t.Fatalf("a body of exactly the limit = %d bytes, %v; want it whole", len(exact), err)
	}

	got, err := io.ReadAll(newLimitedReader(strings.NewReader(strings.Repeat("a", 101)), 100))
	if !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("one byte over the limit gave %v, want errResponseTooLarge", err)
	}
	if len(got) != 100 {
		t.Errorf("read %d bytes before failing, want exactly the limit", len(got))
	}
}

// A stream that never ends used to be read for as long as the provider
// cared to keep sending.
func TestAnEndlessStreamIsCutOffAndReportedAsSuch(t *testing.T) {
	withResponseLimit(t, 8*1024)

	for _, tc := range []struct {
		name  string
		kind  Kind
		frame string
	}{
		{"openai", KindOpenAI, `{"choices":[{"delta":{"content":"xxxxxxxxxxxxxxxxxxxxxxxx"}}]}`},
		{"anthropic", KindAnthropic,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"xxxxxxxxxxxxxxxxxxxxxxxx"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				flusher, _ := w.(http.Flusher)
				// Stops when the client does; the cap is what makes it.
				for i := 0; i < 1_000_000 && r.Context().Err() == nil; i++ {
					if _, err := fmt.Fprintf(w, "data: %s\n\n", tc.frame); err != nil {
						return
					}
					if flusher != nil && i%64 == 0 {
						flusher.Flush()
					}
				}
			}))
			defer server.Close()

			var out collected
			_, err := testRegistry().Chat(context.Background(),
				Provider{Kind: tc.kind, BaseURL: server.URL, APIKey: "k"},
				ChatRequest{
					Model: testModel(), Stream: true,
					Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
				}, out.sink)
			if !isTooLarge(err) {
				t.Fatalf("an endless stream ended with %v, want the size error", err)
			}
			if out.answer.Len() == 0 {
				t.Error("nothing streamed before the cut; the partial answer should be kept")
			}
		})
	}
}

func TestAnOversizedDocumentIsRefused(t *testing.T) {
	withResponseLimit(t, 2*1024)
	big := strings.Repeat("x", 8*1024)

	t.Run("openai chat", func(t *testing.T) {
		server := jsonServer(t, `{"choices":[{"message":{"content":"`+big+`"}}]}`, nil)
		_, err := testRegistry().Chat(context.Background(),
			Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
			ChatRequest{
				Model:    testModel(),
				Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
			}, (&collected{}).sink)
		if !isTooLarge(err) {
			t.Fatalf("got %v, want the size error", err)
		}
	})

	t.Run("anthropic chat", func(t *testing.T) {
		server := jsonServer(t, `{"content":[{"type":"text","text":"`+big+`"}]}`, nil)
		_, err := testRegistry().Chat(context.Background(),
			Provider{Kind: KindAnthropic, BaseURL: server.URL, APIKey: "k"},
			ChatRequest{
				Model:    testModel(),
				Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
			}, (&collected{}).sink)
		if !isTooLarge(err) {
			t.Fatalf("got %v, want the size error", err)
		}
	})

	t.Run("model list", func(t *testing.T) {
		server := jsonServer(t, `{"data":[{"id":"`+big+`"}]}`, nil)
		_, err := testRegistry().ListModels(context.Background(),
			Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"})
		if !isTooLarge(err) {
			t.Fatalf("got %v, want the size error", err)
		}
	})

	t.Run("image generation", func(t *testing.T) {
		server := jsonServer(t, `{"data":[{"b64_json":"`+big+`"}]}`, nil)
		_, err := testRegistry().GenerateImage(context.Background(),
			Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
			ImageRequest{Model: "m", Prompt: "p"})
		if !isTooLarge(err) {
			t.Fatalf("got %v, want the size error", err)
		}
	})
}

// A document that fits is untouched, including one of exactly the limit's size.
func TestAResponseWithinTheCeilingIsReadWhole(t *testing.T) {
	withResponseLimit(t, 4*1024)
	server := jsonServer(t, `{"choices":[{"message":{"content":"`+strings.Repeat("x", 1000)+`"}}]}`, nil)

	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{
			Model:    testModel(),
			Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
		}, (&collected{}).sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if len(result.Text) != 1000 {
		t.Errorf("answer is %d characters, want 1000", len(result.Text))
	}
}

// Fragments are written to a builder, and a call that goes on forever is
// stopped at MaxToolArgumentBytes instead of being allowed to fill memory.
func TestRunawayToolArgumentsFailTheCall(t *testing.T) {
	fragment := strings.Repeat("a", 64*1024)
	count := MaxToolArgumentBytes/len(fragment) + 8

	cases := []struct {
		name  string
		kind  Kind
		start string
		frame string
	}{
		{
			"openai", KindOpenAI,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"lookup","arguments":"{\"q\":\""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"` + fragment + `"}}]}}]}`,
		},
		{
			"anthropic", KindAnthropic,
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t","name":"lookup","input":{}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"` + fragment + `"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frames := []string{tc.start}
			for i := 0; i < count; i++ {
				frames = append(frames, tc.frame)
			}
			server := sseServer(t, frames, nil)

			var out collected
			result, err := testRegistry().Chat(context.Background(),
				Provider{Kind: tc.kind, BaseURL: server.URL, APIKey: "k"},
				ChatRequest{
					Model: testModel(), Stream: true, Tools: toolFixture(),
					Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}},
				}, out.sink)
			var adapterErr *Error
			if !errors.As(err, &adapterErr) || adapterErr.Kind != ErrorUpstream ||
				!strings.Contains(adapterErr.Message, "tool-call arguments") {
				t.Fatalf("got %v, want the tool-argument error", err)
			}
			if len(result.ToolCalls) != 0 || len(out.calls) != 0 {
				t.Errorf("a call that blew the ceiling was still delivered: %+v", result.ToolCalls)
			}
		})
	}
}
