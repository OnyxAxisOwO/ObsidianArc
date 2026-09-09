package adapter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The per-provider timeout an operator sets in the backoffice was read from
// the database, carried on Provider, and used by nothing. These pin what it
// means now, and — more importantly — what it must not mean.

// It bounds getting a response started.
func TestAProviderTimeoutEndsAnUpstreamThatNeverAnswers(t *testing.T) {
	// Released by the test rather than by the request context: a handler that
	// never writes is not told the client has gone, so waiting on r.Context()
	// here would hang the server's own shutdown instead of the client.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		// Headers never sent: the request is accepted and then ignored.
		<-release
	}))
	defer server.Close()
	defer close(release)

	var out collected
	started := time.Now()
	_, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k", Timeout: 300 * time.Millisecond},
		ChatRequest{Model: testModel(), Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
		out.sink)
	if err == nil {
		t.Fatal("a provider that never answered was not given up on")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("gave up after %s; the timeout is 300ms", elapsed)
	}

	var upstream *Error
	if !asAdapterError(err, &upstream) {
		t.Fatalf("error is not an adapter error: %v", err)
	}
	// A timeout is the provider failing, not the reader pressing Stop — the
	// two are counted differently in the ledger.
	if upstream.Kind != ErrorNetwork {
		t.Errorf("kind = %v, want %v", upstream.Kind, ErrorNetwork)
	}
}

// And it must not bound the generation. AGENTS.md is explicit that a deadline
// over the whole call would cut a long answer off mid-sentence, which is why
// this package sets no client-level Timeout; applying the provider's setting
// as one would have reintroduced exactly that.
func TestAProviderTimeoutDoesNotCutAStreamThatOutlastsIt(t *testing.T) {
	const timeout = 250 * time.Millisecond

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		frames := []string{
			`{"choices":[{"delta":{"content":"one "}}]}`,
			`{"choices":[{"delta":{"content":"two "}}]}`,
			`{"choices":[{"delta":{"content":"three"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, frame := range frames {
			fmt.Fprintf(w, "data: %s\n\n", frame)
			if flusher != nil {
				flusher.Flush()
			}
			// Each gap alone is longer than the whole timeout, so a deadline
			// over the call rather than over the first byte kills this.
			select {
			case <-time.After(timeout):
			case <-r.Context().Done():
				return
			}
		}
	}))
	defer server.Close()

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k", Timeout: timeout},
		ChatRequest{Model: testModel(), Stream: true, Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
		out.sink)
	if err != nil {
		t.Fatalf("a stream longer than the provider timeout was cut off: %v", err)
	}
	if result.Text != "one two three" {
		t.Errorf("answer = %q, want %q", result.Text, "one two three")
	}
	if out.answer.String() != result.Text {
		t.Errorf("streamed %q but returned %q", out.answer.String(), result.Text)
	}
}

// A provider with no timeout configured behaves as it always did.
func TestNoProviderTimeoutLeavesTheRequestUnbounded(t *testing.T) {
	frames := []string{
		`{"choices":[{"delta":{"content":"fine"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
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
	if result.Text != "fine" {
		t.Errorf("answer = %q", result.Text)
	}
}
