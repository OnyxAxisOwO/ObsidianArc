package chat

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
)

// Retention drops a picture once the turn carrying it has been dispatched. An
// edit of that message names the picture again. The edit lifts the picture off
// the message it replaces and claims it for the new one, so without the check
// on the claim the edit would be stored with an empty picture and the provider
// call would silently omit it. The send is refused whole instead, and the
// transcript is what it was before the edit.
func TestAnEditNamingADiscardedPictureIsRefusedWhole(t *testing.T) {
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

	// The first turn sends the picture, and dispatch discards its bytes.
	f.upstream.script(`{"choices":[{"delta":{"content":"I see it"}}]}`)
	first, err := f.turn(t, ctx, TurnRequest{Content: "what is this", AttachmentIDs: []string{sent.ID}})
	if err != nil {
		t.Fatal(err)
	}
	conversationID := first.start.ConversationID

	before := f.messages(t, conversationID)
	var question string
	for _, message := range before {
		if message.Role == conversation.RoleUser {
			question = message.ID
		}
	}
	if question == "" {
		t.Fatal("the first turn stored no user message")
	}

	recorder := lab.turn(t, `{"model_id":"`+f.model.ID+`","conversation_id":"`+conversationID+
		`","truncate_from_message_id":"`+question+`","content":"what is this now","attachment_ids":["`+sent.ID+`"]}`)
	body := recorder.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, `"code":"attachment_unavailable"`) {
		t.Fatalf("the edit did not end in attachment_unavailable: %s", body)
	}
	if strings.Contains(body, "event: start") {
		t.Errorf("the edit started a turn before its picture was claimed: %s", body)
	}

	after := f.messages(t, conversationID)
	if !reflect.DeepEqual(after, before) {
		t.Errorf("a refused edit changed the transcript:\nbefore: %+v\nafter:  %+v", before, after)
	}
}
