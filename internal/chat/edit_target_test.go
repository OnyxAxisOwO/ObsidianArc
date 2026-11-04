package chat

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// An edit that names a message no longer in the transcript is answered as the
// 404 it is, before the stream opens. Once it is open the same miss can only
// arrive as a generic failure, which is what a stale tab used to see.
func TestAnEditOfAMessageThatIsGoneIsAnsweredAsNotFoundBeforeTheStream(t *testing.T) {
	lab := newImageLab(t, 0)
	lab.fixture.upstream.script(`{"choices":[{"delta":{"content":"hello back"}}]}`)
	owned, err := lab.fixture.turn(t, context.Background(), TurnRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	conversationID := owned.start.ConversationID

	gone := id.New()
	recorder := lab.turn(t, `{"model_id":"`+lab.fixture.model.ID+`","conversation_id":"`+conversationID+
		`","truncate_from_message_id":"`+gone+`","content":"edited"}`)
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "No such message.") {
		t.Fatalf("status = %d, want 404 for a message that is gone: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Header().Get("Content-Type"), "text/event-stream") {
		t.Error("the stream was opened before the message was checked")
	}
	if messages := lab.fixture.messages(t, conversationID); len(messages) != 2 {
		t.Errorf("a refused edit changed the transcript: %d messages, want 2", len(messages))
	}

	// A new conversation has no messages to rewind to.
	recorder = lab.turn(t, `{"model_id":"`+lab.fixture.model.ID+`","truncate_from_message_id":"`+gone+`","content":"edited"}`)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("a rewind into a new conversation: status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}
