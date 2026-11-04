package conversation

import (
	"context"
	"testing"
)

// An Image Lab picture is kept in the library rather than on a message, and the
// Image Lab documentation says library pictures are still subject to the
// instance's cleanup policy. The age rule and the daily purge both skipped them,
// because they select only attachments that belong to a message.
func TestLibraryPicturesAreSubjectToTheAgeLimitAndTheDailyPurge(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)

	picture := f.libraryPicture(t, daysAgo(30))

	result, err := f.store.Sweep(ctx, Retention{AfterDays: 7}, 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.Aged != 1 {
		t.Fatalf("aged out %d library pictures, want 1", result.Aged)
	}
	if _, _, err := f.store.Blob(ctx, f.userID, picture); err != ErrAttachmentDiscarded {
		t.Errorf("an old library picture is still readable: %v", err)
	}
}

func TestLibraryPicturesGoInTheDailyPurge(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)

	picture := f.libraryPicture(t, daysAgo(0))
	if dropped, err := f.store.DiscardBefore(ctx, daysAgo(-1)); err != nil || dropped != 1 {
		t.Fatalf("daily purge dropped %d (%v), want the library picture", dropped, err)
	}
	if _, _, err := f.store.Blob(ctx, f.userID, picture); err != ErrAttachmentDiscarded {
		t.Errorf("a library picture survived the daily purge: %v", err)
	}
}

// libraryPicture stores a generated image the way the Image Lab does: an upload
// with no message, recorded in the library.
func (f *retentionFixture) libraryPicture(t *testing.T, createdAt int64) string {
	t.Helper()
	ctx := context.Background()

	uploaded, err := f.store.Upload(ctx, UploadInput{
		UserID: f.userID, Mime: "image/png", Data: []byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4},
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := f.store.RecordImageGeneration(ctx, RecordImageGenerationInput{
		UserID: f.userID, AttachmentID: uploaded.ID, ModelID: "test-model", Prompt: "a lamp",
	}); err != nil {
		t.Fatalf("record generation: %v", err)
	}
	if _, err := f.store.db.Exec(ctx,
		`UPDATE attachments SET created_at = ? WHERE id = ?`, createdAt, uploaded.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	return uploaded.ID
}
