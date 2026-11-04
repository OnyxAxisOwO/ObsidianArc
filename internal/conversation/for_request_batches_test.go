package conversation

import (
	"bytes"
	"context"
	"testing"
)

// A whole transcript used to name every message in one IN list, one bind
// parameter each, so a conversation longer than the driver's parameter limit
// failed its turn. The pictures are the part that must still arrive, so one
// sits past the first batch of messages.
func TestForRequestWholeTranscriptPastTheBindLimit(t *testing.T) {
	store, _, account := attachmentFixture(t)
	ctx := context.Background()
	thread, err := store.Create(ctx, nil, account.ID, NewConversation{Title: "Long"})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	const turns = 40000
	// One statement, so seeding does not cost forty thousand round trips.
	if _, err := store.db.Exec(ctx,
		`WITH RECURSIVE turn(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM turn WHERE n < ?)
		 INSERT INTO messages (id, conversation_id, user_id, seq, role, content, reasoning,
		   error, model_id, model_name, provider_id, stats_json, created_at)
		 SELECT 'message-' || n, ?, ?, n, 'user', 'question', '', '', NULL, '', NULL, '', n
		 FROM turn`,
		turns, thread.ID, account.ID); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}

	picture, err := store.Upload(ctx, UploadInput{
		UserID: account.ID, Mime: "image/png", Data: bytes.Repeat([]byte{1}, 32),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := store.db.Exec(ctx,
		`UPDATE attachments SET message_id = ? WHERE id = ?`, "message-1201", picture.ID); err != nil {
		t.Fatalf("attach picture: %v", err)
	}

	got, err := store.ForRequest(ctx, nil, account.ID, thread.ID, 0)
	if err != nil {
		t.Fatalf("whole transcript: %v", err)
	}
	if len(got) != turns {
		t.Fatalf("whole transcript = %d messages, want %d", len(got), turns)
	}
	if got[1200].ID != "message-1201" || len(got[1200].Attachments) != 1 || got[1200].Attachments[0].ID != picture.ID {
		t.Fatalf("picture past the first batch did not come with its message: %+v", got[1200])
	}
}
