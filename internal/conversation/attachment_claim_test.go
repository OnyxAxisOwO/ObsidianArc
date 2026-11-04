package conversation

import (
	"context"
	"errors"
	"testing"
)

// The janitor removes an unattached upload once its window passes. A send that
// names it used to be stored without the picture and with no error, so the
// model answered the previous question. The send is now refused whole, and the
// transaction leaves nothing behind.
func TestSendNamingAnExpiredPictureIsRefusedAndStoresNothing(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)

	uploaded, err := f.store.Upload(ctx, UploadInput{
		UserID: f.userID, Mime: "image/png", Data: []byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4},
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	thread, err := f.store.Create(ctx, nil, f.userID, NewConversation{Title: "thread"})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := f.store.db.Exec(ctx, `DELETE FROM attachments WHERE id = ?`, uploaded.ID); err != nil {
		t.Fatalf("expire the upload: %v", err)
	}

	_, err = f.store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID,
		UserID:         f.userID,
		Role:           RoleUser,
		Content:        "look at this",
		AttachmentIDs:  []string{uploaded.ID},
	})
	if !errors.Is(err, ErrAttachmentUnavailable) {
		t.Fatalf("append with an expired picture: err = %v, want ErrAttachmentUnavailable", err)
	}

	messages, err := f.store.Messages(ctx, nil, f.userID, thread.ID)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("a refused send stored %d message(s)", len(messages))
	}
}

// A picture listed twice in one send is attached once. Claiming it a second
// time found the row already taken, and the whole send was refused with a
// message saying the picture was gone.
func TestSendNamingAPictureTwiceAttachesItOnce(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)

	uploaded, err := f.store.Upload(ctx, UploadInput{
		UserID: f.userID, Mime: "image/png", Data: []byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4},
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	thread, err := f.store.Create(ctx, nil, f.userID, NewConversation{Title: "thread"})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	message, err := f.store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID,
		UserID:         f.userID,
		Role:           RoleUser,
		Content:        "look at this",
		AttachmentIDs:  []string{uploaded.ID, uploaded.ID},
	})
	if err != nil {
		t.Fatalf("append naming one picture twice: %v", err)
	}
	if len(message.Attachments) != 1 {
		t.Fatalf("message carries %d pictures, want 1", len(message.Attachments))
	}
}
