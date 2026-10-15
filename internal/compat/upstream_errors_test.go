package compat

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
)

// A request whose deadline passed is cancelled, not a provider that refused
// it: the caller is told the request was cancelled, the same as a caller that
// went away.
func TestTranslateUpstreamKeepsCancellationAsCancellation(t *testing.T) {
	cancelled := apiError{
		status:  499,
		kind:    "invalid_request_error",
		code:    "cancelled",
		message: "The request was cancelled.",
	}
	for name, err := range map[string]error{
		"the context was cancelled":         context.Canceled,
		"the adapter reports cancellation":  &adapter.Error{Kind: adapter.ErrorCancelled, Message: "The request was cancelled."},
		"a deadline that ended the request": fmt.Errorf("stream: %w", &adapter.Error{Kind: adapter.ErrorCancelled, Message: "The request was cancelled."}),
	} {
		got := translateUpstream(err)
		// The cause rides along for the log, which the caller never sees.
		if got.cause != err {
			t.Errorf("%s: the cause was not kept for the log: %v", name, got.cause)
		}
		got.cause = nil
		if got != cancelled {
			t.Errorf("%s: got %+v, want %+v", name, got, cancelled)
		}
	}
}

// A kind the translation does not list is a rejection, and the caller gets the
// generic sentence rather than the provider's own text, which can name an
// upstream model.
func TestTranslateUpstreamUnlistedKindIsARejectionWithoutProviderText(t *testing.T) {
	got := translateUpstream(&adapter.Error{Kind: adapter.ErrorInvalidRequest, Message: "model gpt-x-internal is not enabled"})
	got.cause = nil
	want := apiError{
		status:  http.StatusBadGateway,
		kind:    "server_error",
		code:    "provider_rejected",
		message: "The provider would not accept this request.",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// The adapter files a stopped turn under its own kind, so the ledger has to read
// that kind as a stop too. Testing only context.Canceled recorded every stop as
// a provider rejection.
func TestTurnOutcomeRecordsAnAdapterCancellationAsAborted(t *testing.T) {
	for name, err := range map[string]error{
		"the context was cancelled":        context.Canceled,
		"the adapter reports cancellation": &adapter.Error{Kind: adapter.ErrorCancelled, Message: "The request was cancelled."},
	} {
		status, code := turnOutcome(err)
		if status != chat.StatusAborted || code != "cancelled" {
			t.Errorf("%s: recorded as %q/%q, want %q/cancelled", name, status, code, chat.StatusAborted)
		}
	}
}

// A provider failure that lands while the client is hanging up is a failure.
// Only a stop explains the request context ending, so the turn is not taken for
// a departure and its error reaches the ledger.
func TestAProviderFailureDuringAHangUpIsStillAFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)

	failure := &adapter.Error{Kind: adapter.ErrorUpstream, Message: "the provider failed"}
	if abandoned(false, r, failure) {
		t.Error("a provider failure was taken for the client leaving")
	}
	if !abandoned(false, r, context.Canceled) {
		t.Error("a stopped turn was not taken for the client leaving")
	}
	if !abandoned(true, r, failure) {
		t.Error("a write that failed did not count as the client leaving")
	}
}

// errorLines returns the records written at ERROR level. Counting those, rather
// than one message, is what catches a fault written twice under two sentences.
func errorLines(logged string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(logged), "\n") {
		if strings.Contains(line, "level=ERROR") {
			out = append(out, line)
		}
	}
	return out
}

// A provider fault used to be written twice: once by the translation, and again
// by the renderer, which had no cause left to give. Each path must write it
// once, with its cause.
func TestAProviderFaultIsLoggedOnceWithItsCause(t *testing.T) {
	bodies := map[string]string{
		"buffered": `{"model":"MODEL","stream":false,"messages":[{"role":"user","content":"hi"}]}`,
		"streamed": `{"model":"MODEL","stream":true,"messages":[{"role":"user","content":"hi"}]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			var logged bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			f := newFixture(t)
			f.upstream.fail(http.StatusInternalServerError, `{"error":{"message":"the upstream exploded"}}`)
			f.do(t, http.MethodPost, "/v1/chat/completions", f.token,
				strings.ReplaceAll(body, "MODEL", f.model.ID))

			faults := errorLines(logged.String())
			if len(faults) != 1 {
				t.Fatalf("provider fault logged %d times, want once:\n%s", len(faults), logged.String())
			}
			if strings.Contains(faults[0], "<nil>") || !strings.Contains(faults[0], "error=") {
				t.Errorf("the logged fault has no cause: %s", faults[0])
			}
		})
	}
}
