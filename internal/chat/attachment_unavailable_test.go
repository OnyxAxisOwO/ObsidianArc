package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
)

// The claim runs inside the turn, after the stream has opened, so a picture
// that cannot be claimed reaches the browser as an error event rather than a
// 409. The event must carry the code the composer acts on, and the transcript
// must be exactly what it was before the send.
func TestAnUnavailablePictureOnAnOpenStreamIsAnsweredWithItsCode(t *testing.T) {
	lab := newImageLab(t, 0)
	f := lab.fixture
	ctx := context.Background()
	pixel := []byte{0x89, 0x50, 0x4e, 0x47, 1, 2, 3, 4}

	sent, err := f.conversations.Upload(ctx, conversation.UploadInput{
		UserID: f.account.ID, Mime: "image/png", Width: 2, Height: 2, Data: pixel,
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := f.conversations.Upload(ctx, conversation.UploadInput{
		UserID: f.other.ID, Mime: "image/png", Width: 2, Height: 2, Data: pixel,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The first turn claims the picture, so the second send names one already sent.
	f.upstream.script(`{"choices":[{"delta":{"content":"I see it"}}]}`)
	first, err := f.turn(t, ctx, TurnRequest{Content: "what is this", AttachmentIDs: []string{sent.ID}})
	if err != nil {
		t.Fatal(err)
	}
	conversationID := first.start.ConversationID

	for _, attachmentID := range []string{sent.ID, foreign.ID} {
		recorder := lab.turn(t, `{"model_id":"`+f.model.ID+`","conversation_id":"`+conversationID+
			`","content":"again","attachment_ids":["`+attachmentID+`"]}`)
		body := recorder.Body.String()
		if !strings.Contains(body, "event: error") || !strings.Contains(body, `"code":"attachment_unavailable"`) {
			t.Errorf("picture %s: the stream did not end in attachment_unavailable: %s", attachmentID, body)
		}
		if strings.Contains(body, "event: start") {
			t.Errorf("picture %s: the turn started before its picture was claimed", attachmentID)
		}
		if messages := f.messages(t, conversationID); len(messages) != 2 {
			t.Errorf("picture %s: a refused send changed the transcript: %d messages, want 2", attachmentID, len(messages))
		}
	}
}
