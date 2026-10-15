package conversation

import (
	"context"
	"fmt"
	"testing"
)

// The Image Lab page after the first was cut on the timestamp alone, so rows
// that shared the boundary millisecond were dropped on the way.
func TestImageLabPagesDoNotLoseRowsThatShareAMillisecond(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)
	for i := 0; i < 3; i++ {
		uploaded, err := f.store.Upload(ctx, UploadInput{
			UserID: f.userID, Mime: "image/png", Data: []byte{0x89, 'P', 'N', 'G', 1, 2, 3, byte(i)},
		})
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		if _, err := f.store.RecordImageGeneration(ctx, RecordImageGenerationInput{
			UserID: f.userID, AttachmentID: uploaded.ID, ModelID: "test-model",
			Prompt: fmt.Sprintf("prompt %d", i),
		}); err != nil {
			t.Fatalf("record generation: %v", err)
		}
	}
	// The three rows are written in consecutive milliseconds on a fast machine
	// and in one on a slow one; forcing the tie makes the test deterministic.
	if _, err := f.store.db.Exec(ctx,
		`UPDATE image_generations SET created_at = ? WHERE user_id = ?`, int64(1000), f.userID); err != nil {
		t.Fatalf("make the rows share a millisecond: %v", err)
	}

	seen := map[string]bool{}
	var before int64
	var beforeID string
	for page := 0; page < 5; page++ {
		list, err := f.store.ListImageGenerations(ctx, f.userID, 1, before, beforeID)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(list) == 0 {
			break
		}
		for _, generation := range list {
			if seen[generation.ID] {
				t.Fatalf("page %d repeated generation %s", page, generation.ID)
			}
			seen[generation.ID] = true
		}
		last := list[len(list)-1]
		before, beforeID = last.CreatedAt, last.ID
	}
	if len(seen) != 3 {
		t.Fatalf("paged through %d generations, want 3", len(seen))
	}
}
