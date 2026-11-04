package conversation

import (
	"context"
	"errors"
	"testing"
)

// A library picture has no message, which is the same shape a fresh composer
// upload has. Claiming it into a chat would hand its bytes to the turn's
// dispatch and leave the library entry pointing at a discarded picture.
func TestLibraryPictureIsNotClaimedIntoAMessage(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)
	picture := f.libraryPicture(t, daysAgo(0))

	thread, err := f.store.Create(ctx, nil, f.userID, NewConversation{Title: "thread"})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	// Refused rather than skipped: a send that names a picture it cannot have
	// must not go out without it and say nothing.
	_, err = f.store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID,
		UserID:         f.userID,
		Role:           RoleUser,
		Content:        "use this",
		AttachmentIDs:  []string{picture},
	})
	if !errors.Is(err, ErrAttachmentUnavailable) {
		t.Fatalf("append with a library picture: err = %v, want ErrAttachmentUnavailable", err)
	}
	messages, err := f.store.Messages(ctx, nil, f.userID, thread.ID)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("a refused send stored %d message(s)", len(messages))
	}
	if _, _, err := f.store.Blob(ctx, f.userID, picture); err != nil {
		t.Fatalf("the library picture is no longer readable after the send: %v", err)
	}
}

// Deleting a library entry must not remove a picture a chat already holds.
// Before the fix the attachment went regardless of the message that owned it.
func TestDeletingALibraryEntryKeepsThePictureInATranscript(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)
	picture := f.attach(t, daysAgo(0))

	gen, err := f.store.RecordImageGeneration(ctx, RecordImageGenerationInput{
		UserID: f.userID, AttachmentID: picture, ModelID: "test-model", Prompt: "a lamp",
	})
	if err != nil {
		t.Fatalf("record generation: %v", err)
	}
	if err := f.store.DeleteImageGeneration(ctx, f.userID, gen.ID); err != nil {
		t.Fatalf("delete generation: %v", err)
	}
	if _, _, err := f.store.Blob(ctx, f.userID, picture); err != nil {
		t.Fatalf("deleting the library entry removed the transcript's picture: %v", err)
	}
}

// PostgreSQL refuses a NUL byte in a text value, and the Image Lab handler does
// not strip one, so the prompt has to be cleaned here the way a message is.
func TestRecordImageGenerationStoresTextWithoutNULBytes(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)

	uploaded, err := f.store.Upload(ctx, UploadInput{
		UserID: f.userID, Mime: "image/png", Data: []byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4},
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := f.store.RecordImageGeneration(ctx, RecordImageGenerationInput{
		UserID: f.userID, AttachmentID: uploaded.ID, ModelID: "test-model",
		Prompt: "a\x00lamp", RevisedPrompt: "re\x00vised", Size: "1\x00024", Style: "vi\x00vid",
	}); err != nil {
		t.Fatalf("record a prompt with a NUL byte: %v", err)
	}

	list, err := f.store.ListImageGenerations(ctx, f.userID, 10, 0, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d generations, want 1", len(list))
	}
	got := list[0]
	if got.Prompt != "alamp" || got.RevisedPrompt != "revised" || got.Size != "1024" || got.Style != "vivid" {
		t.Fatalf("stored text %q %q %q %q, want the NUL bytes removed", got.Prompt, got.RevisedPrompt, got.Size, got.Style)
	}
}

// The lab names a download by the picture's bytes, so both the first page and
// a cursor page must say what the picture is. A JPEG listed without its mime
// would be saved as .png.
func TestListedGenerationCarriesThePicturesMime(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)

	uploaded, err := f.store.Upload(ctx, UploadInput{
		UserID: f.userID, Mime: "image/jpeg", Data: []byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3, 4},
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := f.store.RecordImageGeneration(ctx, RecordImageGenerationInput{
		UserID: f.userID, AttachmentID: uploaded.ID, ModelID: "test-model", Prompt: "a lamp",
	}); err != nil {
		t.Fatalf("record generation: %v", err)
	}

	first, err := f.store.ListImageGenerations(ctx, f.userID, 10, 0, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(first) != 1 || first[0].Mime != "image/jpeg" {
		t.Fatalf("first page = %+v, want one generation with mime image/jpeg", first)
	}

	page, err := f.store.ListImageGenerations(ctx, f.userID, 10, first[0].CreatedAt+1, first[0].ID)
	if err != nil {
		t.Fatalf("cursor page: %v", err)
	}
	if len(page) != 1 || page[0].Mime != "image/jpeg" {
		t.Fatalf("cursor page = %+v, want one generation with mime image/jpeg", page)
	}
}
