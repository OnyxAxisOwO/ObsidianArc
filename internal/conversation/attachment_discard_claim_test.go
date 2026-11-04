package conversation

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

func uploadedPicture(t *testing.T, f *retentionFixture) string {
	t.Helper()
	uploaded, err := f.store.Upload(context.Background(), UploadInput{
		UserID: f.userID, Mime: "image/png", Data: []byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4},
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	return uploaded.ID
}

// An edit lifts a picture off the message it is replacing, then claims it for
// the new one. Once retention has dropped the bytes, the claim must refuse:
// otherwise the edit is stored with a picture the provider never receives.
func TestAnEditCannotReclaimADiscardedPicture(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)
	pictureID := uploadedPicture(t, f)
	thread, err := f.store.Create(ctx, nil, f.userID, NewConversation{Title: "thread"})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	original, err := f.store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID, UserID: f.userID, Role: RoleUser, Content: "look",
		AttachmentIDs: []string{pictureID},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := f.store.db.Exec(ctx,
		`UPDATE attachments SET discarded_at = ? WHERE id = ?`, int64(1), pictureID); err != nil {
		t.Fatalf("discard the picture: %v", err)
	}

	err = f.store.db.Tx(ctx, func(tx *database.Tx) error {
		if err := f.store.DetachPictures(ctx, tx, f.userID, thread.ID, original.ID, []string{pictureID}); err != nil {
			return err
		}
		_, err := f.store.Append(ctx, tx, AppendInput{
			ConversationID: thread.ID, UserID: f.userID, Role: RoleUser, Content: "again",
			AttachmentIDs: []string{pictureID},
		})
		return err
	})
	if !errors.Is(err, ErrAttachmentUnavailable) {
		t.Fatalf("edit with a discarded picture: err = %v, want ErrAttachmentUnavailable", err)
	}

	messages, err := f.store.Messages(ctx, nil, f.userID, thread.ID)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("a refused edit left %d messages, want the original 1", len(messages))
	}
	var owner sql.NullString
	if err := f.store.db.QueryRow(ctx, `SELECT message_id FROM attachments WHERE id = ?`, pictureID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if !owner.Valid || owner.String != original.ID {
		t.Fatalf("the refused edit moved the picture: owner = %v, want %s", owner, original.ID)
	}
}

// A repeated id is not a second picture, so it must not spend the budget. Six
// distinct pictures named with one repeat is a valid send.
func TestRepeatedIDsDoNotUseUpTheBudget(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)
	thread, err := f.store.Create(ctx, nil, f.userID, NewConversation{Title: "thread"})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	var ids []string
	for i := 0; i < MaxAttachmentsPerMessage; i++ {
		ids = append(ids, uploadedPicture(t, f))
	}
	named := append([]string{ids[0]}, ids...)

	message, err := f.store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID, UserID: f.userID, Role: RoleUser, Content: "look",
		AttachmentIDs: named,
	})
	if err != nil {
		t.Fatalf("send with a repeated picture: %v", err)
	}
	if len(message.Attachments) != MaxAttachmentsPerMessage {
		t.Fatalf("stored %d pictures, want all %d distinct ones", len(message.Attachments), MaxAttachmentsPerMessage)
	}
}

// More distinct pictures than a message may hold is refused whole. Cutting the
// list would send the message without the pictures past the cap, and nobody
// would be told.
func TestTooManyDistinctPicturesRefusesTheSend(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)
	thread, err := f.store.Create(ctx, nil, f.userID, NewConversation{Title: "thread"})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	var ids []string
	for i := 0; i < MaxAttachmentsPerMessage+1; i++ {
		ids = append(ids, uploadedPicture(t, f))
	}

	if err := f.store.DetachPictures(ctx, nil, f.userID, thread.ID, "", ids); !errors.Is(err, ErrTooManyAttachments) {
		t.Fatalf("detach of too many pictures: err = %v, want ErrTooManyAttachments", err)
	}
	_, err = f.store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID, UserID: f.userID, Role: RoleUser, Content: "look",
		AttachmentIDs: ids,
	})
	if !errors.Is(err, ErrTooManyAttachments) {
		t.Fatalf("send with too many pictures: err = %v, want ErrTooManyAttachments", err)
	}

	messages, err := f.store.Messages(ctx, nil, f.userID, thread.ID)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("a refused send stored %d message(s)", len(messages))
	}
}
