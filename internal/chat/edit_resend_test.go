package chat

import (
	"context"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// Editing a message that has a picture and sending it again must keep the
// picture. The edit truncates the old message, and the truncation used to
// cascade into the picture's row before the resend could claim it, so the new
// message was stored and answered without it and nothing reported the loss.
func TestEditAndResendKeepsTheMessagesPictures(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// With retention on the bytes survive the first turn, so the resend has
	// something real to carry.
	if err := f.settings.Set(ctx, settings.AttachmentRetain, "true"); err != nil {
		t.Fatal(err)
	}

	pixel := []byte{0x89, 0x50, 0x4e, 0x47, 1, 2, 3, 4}
	uploaded, err := f.conversations.Upload(ctx, conversation.UploadInput{
		UserID: f.account.ID, Mime: "image/png", Width: 2, Height: 2, Data: pixel,
	})
	if err != nil {
		t.Fatal(err)
	}

	f.upstream.script(`{"choices":[{"delta":{"content":"first look"}}]}`)
	first, err := f.turn(t, ctx, TurnRequest{
		Content:       "what is this",
		AttachmentIDs: []string{uploaded.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	conversationID := first.start.ConversationID
	original := f.messages(t, conversationID)[0]
	if len(original.Attachments) != 1 {
		t.Fatalf("the first message lost its picture before the edit: %+v", original)
	}

	f.upstream.script(`{"choices":[{"delta":{"content":"second look"}}]}`)
	if _, err := f.turn(t, ctx, TurnRequest{
		ConversationID:        conversationID,
		TruncateFromMessageID: original.ID,
		Content:               "what is this, again",
		AttachmentIDs:         []string{uploaded.ID},
	}); err != nil {
		t.Fatal(err)
	}

	messages := f.messages(t, conversationID)
	if len(messages) != 2 || messages[0].Content != "what is this, again" {
		t.Fatalf("after editing: %+v", messages)
	}
	if len(messages[0].Attachments) != 1 {
		t.Fatalf("the edited message has %d pictures, want the one it was sent with", len(messages[0].Attachments))
	}

	images := 0
	sent, _ := f.upstream.lastRequest()["messages"].([]any)
	for _, entry := range sent {
		message, _ := entry.(map[string]any)
		parts, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			block, _ := part.(map[string]any)
			if block["type"] == "image_url" {
				images++
			}
		}
	}
	if images != 1 {
		t.Errorf("the edited answer carried the picture %d times, want 1", images)
	}
}
