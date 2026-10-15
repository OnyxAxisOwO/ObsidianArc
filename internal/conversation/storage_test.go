package conversation

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func TestStorageTableIncludesEveryAccount(t *testing.T) {
	store, users, _ := attachmentFixture(t)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		account, err := users.Create(ctx, nil, user.CreateInput{Username: fmt.Sprintf("storage-%02d", i), PasswordHash: "unused"})
		if err != nil {
			t.Fatal(err)
		}
		uploadBytes(t, store, account.ID, i+1)
	}
	rows, err := store.referenceHeldByUser(ctx, 0)
	if err != nil || len(rows) != 25 {
		t.Fatalf("storage breakdown: %d, %v", len(rows), err)
	}
	if rows[0].Bytes != 25 || rows[24].Bytes != 1 {
		t.Fatal("storage ranking lost")
	}
}

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

	held, err := store.referenceHeldByUser(ctx, 20)
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

	// The page reads Storage now, and Storage sorts in Go. Its order is checked
	// here against the reference, where the two accounts rank by bytes rather
	// than by count.
	got, err := store.Storage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.ByUser, held) {
		t.Errorf("Storage breakdown = %+v, want %+v", got.ByUser, held)
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
	discarded, err := store.referenceDiscarded(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if discarded != 2 {
		t.Errorf("counted %d discarded rows, want 2", discarded)
	}
	held, err := store.referenceHeldByUser(ctx, 20)
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

// Storage replaces three separate reads, so it must agree with each of them.
// An account whose every file is discarded counts toward the discarded total
// and is absent from the breakdown, as referenceHeldByUser leaves it.
func TestStorageAgreesWithTheReadsItReplaces(t *testing.T) {
	store, users, first := attachmentFixture(t)
	ctx := context.Background()
	second, err := users.Create(ctx, nil, user.CreateInput{Username: "second", PasswordHash: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	uploadBytes(t, store, first.ID, 300)
	uploadBytes(t, store, first.ID, 500)
	uploadBytes(t, store, second.ID, 700)
	uploadBytes(t, store, second.ID, 900)
	if _, err := store.db.Exec(ctx,
		`UPDATE attachments SET data = ?, discarded_at = ? WHERE user_id = ?`,
		[]byte{}, int64(1), second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx,
		`UPDATE attachments SET data = ?, discarded_at = ? WHERE user_id = ? AND size = ?`,
		[]byte{}, int64(1), first.ID, int64(300)); err != nil {
		t.Fatal(err)
	}

	held, bytesHeld, err := store.Held(ctx)
	if err != nil {
		t.Fatal(err)
	}
	discarded, err := store.referenceDiscarded(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byUser, err := store.referenceHeldByUser(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.Storage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.HeldCount != held || got.HeldBytes != bytesHeld || got.Discarded != discarded {
		t.Errorf("storage = %d files / %d bytes / %d discarded, want %d / %d / %d",
			got.HeldCount, got.HeldBytes, got.Discarded, held, bytesHeld, discarded)
	}
	if !reflect.DeepEqual(got.ByUser, byUser) {
		t.Errorf("breakdown = %+v, want %+v", got.ByUser, byUser)
	}
	if len(got.ByUser) != 1 || got.ByUser[0].UserID != first.ID || got.Discarded != 3 {
		t.Errorf("only the account still holding files should be listed, and three rows are discarded: %+v / %d", got.ByUser, got.Discarded)
	}
}

// referenceHeldByUser is the per-account query that Storage replaced. The page
// no longer runs it, so it is kept here as the answer Storage is compared with;
// without that, a drift in Storage would go unseen. Zero means all accounts.
func (s *Store) referenceHeldByUser(ctx context.Context, limit int) ([]UserStorage, error) {
	query := `
		SELECT a.user_id, MAX(COALESCE(u.username, a.user_id)), COUNT(*), COALESCE(SUM(a.size), 0)
		FROM attachments a
		LEFT JOIN users u ON u.id = a.user_id
		WHERE a.discarded_at = 0
		GROUP BY a.user_id
		ORDER BY COALESCE(SUM(a.size), 0) DESC, a.user_id`
	var args []any
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("conversation: held by user: %w", err)
	}
	defer func() { _ = rows.Close() }()

	held := []UserStorage{}
	for rows.Next() {
		var entry UserStorage
		if err := rows.Scan(&entry.UserID, &entry.Name, &entry.Count, &entry.Bytes); err != nil {
			return nil, fmt.Errorf("conversation: held by user: %w", err)
		}
		held = append(held, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conversation: held by user: %w", err)
	}
	return held, nil
}

// referenceDiscarded counts the rows whose bytes have been dropped, the figure
// Storage returns beside the held total. Such a row still names a message; what
// it no longer costs is storage.
func (s *Store) referenceDiscarded(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM attachments WHERE discarded_at <> 0`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("conversation: discarded attachments: %w", err)
	}
	return count, nil
}
