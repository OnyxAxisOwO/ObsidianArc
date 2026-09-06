package conversation

import (
	"bytes"
	"context"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The resources page answers "who is filling the disk", which a column of
// ULIDs does not, and which a total does not either.
func TestHeldByUserRanksTheHeaviestAccountsFirst(t *testing.T) {
	store, users, first := attachmentFixture(t)
	ctx := context.Background()

	second, err := users.Create(ctx, nil, user.CreateInput{
		Username: "light", PasswordHash: "not-a-real-hash",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Sizes chosen so the order by bytes and the order by count disagree: the
	// account with more files holds fewer bytes, which is the case a ranking
	// done in the caller after a cap gets wrong.
	uploadBytes(t, store, first.ID, 4000)
	uploadBytes(t, store, second.ID, 300)
	uploadBytes(t, store, second.ID, 300)
	uploadBytes(t, store, second.ID, 300)

	held, err := store.HeldByUser(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 2 {
		t.Fatalf("listed %d accounts, want 2", len(held))
	}
	if held[0].UserID != first.ID {
		t.Errorf("ranked %q first, want the account holding more bytes", held[0].Name)
	}
	if held[0].Bytes != 4000 || held[0].Count != 1 {
		t.Errorf("first = %d bytes in %d files, want 4000 in 1", held[0].Bytes, held[0].Count)
	}
	if held[1].Bytes != 900 || held[1].Count != 3 {
		t.Errorf("second = %d bytes in %d files, want 900 in 3", held[1].Bytes, held[1].Count)
	}

	// The name, not the id: it is the whole reason the join is there.
	if held[0].Name != "uploader" || held[1].Name != "light" {
		t.Errorf("named %q and %q", held[0].Name, held[1].Name)
	}

	// And the total agrees with the sum of the parts.
	count, size, err := store.Held(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 || size != 4900 {
		t.Errorf("Held = %d files / %d bytes, want 4 / 4900", count, size)
	}
}

// Bytes that retention has already dropped are not storage any more. The row
// survives — it still names a message — so counting rows would report a disk
// that never empties however often the purge runs.
func TestDiscardedRowsLeaveTheStorageFigure(t *testing.T) {
	store, _, account := attachmentFixture(t)
	ctx := context.Background()

	uploadBytes(t, store, account.ID, 2000)
	uploadBytes(t, store, account.ID, 2000)

	if _, err := store.db.Exec(ctx,
		`UPDATE attachments SET data = ?, discarded_at = ? WHERE size = ?`,
		[]byte{}, int64(1), int64(2000)); err != nil {
		t.Fatal(err)
	}

	count, size, err := store.Held(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || size != 0 {
		t.Errorf("still holding %d files / %d bytes after a purge", count, size)
	}
	discarded, err := store.Discarded(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if discarded != 2 {
		t.Errorf("counted %d discarded rows, want 2", discarded)
	}
	held, err := store.HeldByUser(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 0 {
		t.Errorf("an account with nothing left still appears: %+v", held)
	}
}

func uploadBytes(t *testing.T, store *Store, userID string, size int) {
	t.Helper()
	if _, err := store.Upload(context.Background(), UploadInput{
		UserID: userID,
		Mime:   "image/png",
		Data:   bytes.Repeat([]byte{7}, size),
	}); err != nil {
		t.Fatalf("upload %d bytes: %v", size, err)
	}
}
