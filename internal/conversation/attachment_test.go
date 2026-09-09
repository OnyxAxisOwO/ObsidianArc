package conversation

import (
	"bytes"
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A per-file ceiling bounds nothing on its own: uploading is a database write
// any signed-in account can repeat, and six megabytes at a time fills a disk.
// These are the account-level bounds.

func attachmentFixture(t *testing.T) (*Store, *user.Store, user.User) {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "attachments.db"),
		MaxOpenConns: 4,
		MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	groups := group.NewStore(db)
	if _, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	users := user.NewStore(db)
	account, err := users.Create(ctx, nil, user.CreateInput{
		Username: "uploader", PasswordHash: "not-a-real-hash",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return NewStore(db), users, account
}

func upload(t *testing.T, store *Store, userID string, size int) error {
	t.Helper()
	_, err := store.Upload(context.Background(), UploadInput{
		UserID: userID,
		Mime:   "image/png",
		Data:   bytes.Repeat([]byte{1}, size),
	})
	return err
}

func TestPendingUploadsAreCapped(t *testing.T) {
	store, _, account := attachmentFixture(t)

	for i := 0; i < MaxPendingAttachments; i++ {
		if err := upload(t, store, account.ID, 32); err != nil {
			t.Fatalf("upload %d of an allowed %d: %v", i+1, MaxPendingAttachments, err)
		}
	}
	if err := upload(t, store, account.ID, 32); err != ErrTooManyPending {
		t.Fatalf("err = %v, want ErrTooManyPending", err)
	}
}

func TestParallelUploadsCannotRacePastThePendingCap(t *testing.T) {
	store, _, account := attachmentFixture(t)

	const attempts = MaxPendingAttachments * 2
	start := make(chan struct{})
	errorsByAttempt := make(chan error, attempts)
	var workers sync.WaitGroup
	for i := 0; i < attempts; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := store.Upload(context.Background(), UploadInput{
				UserID: account.ID,
				Mime:   "image/png",
				Data:   bytes.Repeat([]byte{1}, 32),
			})
			errorsByAttempt <- err
		}()
	}
	close(start)
	workers.Wait()
	close(errorsByAttempt)

	accepted := 0
	refused := 0
	for err := range errorsByAttempt {
		switch err {
		case nil:
			accepted++
		case ErrTooManyPending:
			refused++
		default:
			t.Fatalf("parallel upload returned %v", err)
		}
	}
	if accepted != MaxPendingAttachments || refused != attempts-MaxPendingAttachments {
		t.Fatalf("accepted %d and refused %d; want %d and %d",
			accepted, refused, MaxPendingAttachments, attempts-MaxPendingAttachments)
	}
}

func TestUploadCapsAreScopedToTheAccount(t *testing.T) {
	store, users, account := attachmentFixture(t)

	for i := 0; i < MaxPendingAttachments; i++ {
		if err := upload(t, store, account.ID, 32); err != nil {
			t.Fatal(err)
		}
	}
	other, err := users.Create(context.Background(), nil, user.CreateInput{
		Username: "another", PasswordHash: "not-a-real-hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := upload(t, store, other.ID, 32); err != nil {
		t.Fatalf("one account's uploads blocked another: %v", err)
	}
}

func TestOversizeFileIsStillRefused(t *testing.T) {
	store, _, account := attachmentFixture(t)

	if err := upload(t, store, account.ID, MaxAttachmentBytes+1); err != ErrAttachmentTooLarge {
		t.Fatalf("err = %v, want ErrAttachmentTooLarge", err)
	}
	if err := upload(t, store, account.ID, 0); err != ErrAttachmentTooLarge {
		t.Fatalf("empty upload err = %v, want ErrAttachmentTooLarge", err)
	}
}

func TestUnsupportedTypeIsRefusedBeforeAnythingIsStored(t *testing.T) {
	store, _, account := attachmentFixture(t)

	_, err := store.Upload(context.Background(), UploadInput{
		UserID: account.ID,
		Mime:   "application/zip",
		Data:   []byte{1, 2, 3},
	})
	if err != ErrUnsupportedMedia {
		t.Fatalf("err = %v, want ErrUnsupportedMedia", err)
	}
}

// A discarded attachment keeps its size so the transcript can still say how
// big the picture was, but its bytes are gone. Counting those against the
// account's allowance filled every account up with images the database no
// longer holds — and because the retention policy discards after the turn
// that sends them, that is nearly every image anybody ever sent.
func TestDiscardedAttachmentsDoNotCountTowardsTheAccountQuota(t *testing.T) {
	store, _, account := attachmentFixture(t)
	ctx := context.Background()

	first, err := store.Upload(ctx, UploadInput{
		UserID: account.ID, Mime: "image/png", Data: bytes.Repeat([]byte{1}, 1024),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Discard by id, the way a dispatched turn releases the bytes it sent.
	if _, err := store.db.Exec(ctx,
		`UPDATE attachments SET data = ?, discarded_at = ? WHERE id = ?`,
		[]byte{}, int64(1), first.ID); err != nil {
		t.Fatal(err)
	}

	_, held, err := store.usage(ctx, nil, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if held != 0 {
		t.Errorf("a discarded attachment still occupies %d bytes of the allowance", held)
	}

	// And the size survives for everything that legitimately reads it back.
	var size int64
	if err := store.db.QueryRow(ctx,
		`SELECT size FROM attachments WHERE id = ?`, first.ID).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if size != 1024 {
		t.Errorf("size = %d, want 1024: the transcript and the storage page both read it", size)
	}
}
