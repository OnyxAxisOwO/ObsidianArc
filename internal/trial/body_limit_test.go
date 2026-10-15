package trial

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Chinese text is three bytes a rune, so a conversation at the character limit
// is about 72 KB on the wire. The old 64 KiB cap refused it with a size error
// before the character count ever ran.
func TestAConversationAtTheCharacterLimitFitsTheBodyCap(t *testing.T) {
	var messages []message
	remaining := MaxTotalChars
	for i := 0; remaining > 0; i++ {
		n := min(MaxMessageChars, remaining)
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		messages = append(messages, message{Role: role, Content: strings.Repeat("字", n)})
		remaining -= n
	}
	payload, err := json.Marshal(request{Messages: messages, Continuation: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) <= 64*1024 {
		t.Fatalf("the test conversation is %d bytes, too small to exercise the cap", len(payload))
	}

	r := httptest.NewRequest("POST", "/api/trial/chat", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	var body request
	if err := httpx.DecodeJSON(w, r, &body, maxTrialBodyBytes); err != nil {
		t.Fatalf("a %d-byte conversation at the character limit was refused: %v", len(payload), err)
	}
}

// The body cap leaves room for framing only if the message count is bounded
// too, so a conversation with more messages than a capped trial can reach is
// refused as too long.
func TestAConversationWithTooManyMessagesIsRefused(t *testing.T) {
	messages := make([]message, maxTrialMessages+1)
	for i := range messages {
		messages[i] = message{Role: "user", Content: "hi"}
	}
	h := &Handlers{signer: newSigner([]byte("test-secret"))}
	if _, err := h.validate(request{Messages: messages}); err == nil {
		t.Fatalf("a conversation of %d messages was accepted", len(messages))
	}
}
