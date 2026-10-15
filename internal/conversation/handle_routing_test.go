package conversation

import (
	"context"
	"database/sql"
	"testing"
)

// A detach handed no transaction still takes the conversation lock, as one
// handed a transaction does. This runs on the default engine and checks only the
// outcome: the lock interleaving, which is what the routing protects, needs
// PostgreSQL (see truncate_lock_test.go).
func TestDetachWithoutATransactionLiftsThePictureOffTheTail(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)
	uploaded, err := f.store.Upload(ctx, UploadInput{
		UserID: f.userID, Mime: "image/png", Data: []byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := f.store.Create(ctx, nil, f.userID, NewConversation{Title: "thread"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := f.store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID, UserID: f.userID, Role: RoleUser, Content: "look",
		AttachmentIDs: []string{uploaded.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := f.store.DetachPictures(ctx, nil, f.userID, thread.ID, message.ID, []string{uploaded.ID}); err != nil {
		t.Fatalf("detach without a transaction: %v", err)
	}
	var owner sql.NullString
	if err := f.store.db.QueryRow(ctx, `SELECT message_id FROM attachments WHERE id = ?`, uploaded.ID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner.Valid {
		t.Fatalf("the picture is still on message %s after the detach", owner.String)
	}
}
