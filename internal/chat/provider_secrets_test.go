package chat

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
)

// Gateways sometimes echo credentials in diagnostic text. Exact replacement
// cannot protect a key the gateway encoded, split, or partially masked.
func TestProviderDiagnosticsDoNotReachTheChatOrTranscript(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("sk-test"))
	for _, stream := range []bool{false, true} {
		f := newFixture(t)
		if stream {
			f.upstream.script(`{"error":{"message":"diagnostic credential ` + encoded + `"}}`)
		} else {
			f.upstream.fail(http.StatusBadRequest, `{"error":{"message":"diagnostic credential `+encoded+`"}}`)
		}
		out, err := f.turn(t, context.Background(), TurnRequest{Content: "hello", Stream: stream})
		if err != nil {
			t.Fatal(err)
		}
		if out.failure == nil {
			t.Fatal("expected a provider failure")
		}
		if strings.Contains(out.failure.Message, encoded) {
			t.Error("the response exposed an encoded provider credential")
		}
		for _, message := range f.messages(t, out.start.ConversationID) {
			if strings.Contains(message.Error, encoded) {
				t.Error("the transcript stored an encoded provider credential")
			}
		}
	}
}

func TestDescribeDoesNotExposeProviderDiagnostics(t *testing.T) {
	for _, kind := range []adapter.ErrorKind{
		adapter.ErrorAuth, adapter.ErrorRateLimit, adapter.ErrorInvalidRequest,
		adapter.ErrorImagesUnsupported, adapter.ErrorRefusal, adapter.ErrorUpstream, adapter.ErrorNetwork,
	} {
		_, message := Describe(&adapter.Error{Kind: kind, Message: "private diagnostic"})
		if strings.Contains(message, "private diagnostic") {
			t.Errorf("%s exposed provider diagnostic text", kind)
		}
	}
}
