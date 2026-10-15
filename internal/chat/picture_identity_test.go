package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
)

// Two different pictures of the same length that agree at both ends share a
// fingerprint. Both must still reach the provider: the fingerprint narrows the
// comparison, and only identical bytes are the same picture.
func TestPicturesSharingAFingerprintAreNotMistakenForOneAnother(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	head := strings.Repeat("h", 64)
	tail := strings.Repeat("t", 64)
	var ids []string
	for _, middle := range []string{"first picture", "other picture"} {
		uploaded, err := f.conversations.Upload(ctx, conversation.UploadInput{
			UserID: f.account.ID, Mime: "image/png", Width: 2, Height: 2,
			Data: []byte(head + middle + tail),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, uploaded.ID)
	}

	f.upstream.script(`{"choices":[{"delta":{"content":"two pictures"}}]}`)
	if _, err := f.turn(t, ctx, TurnRequest{Content: "compare these", AttachmentIDs: ids}); err != nil {
		t.Fatal(err)
	}

	images := 0
	sent, _ := f.upstream.lastRequest()["messages"].([]any)
	for _, entry := range sent {
		message, _ := entry.(map[string]any)
		parts, _ := message["content"].([]any)
		for _, part := range parts {
			if block, _ := part.(map[string]any); block["type"] == "image_url" {
				images++
			}
		}
	}
	if images != 2 {
		t.Errorf("the provider received %d pictures, want both of them", images)
	}
}
