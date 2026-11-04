package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A rewind reads the position it deletes from, so it holds the conversation's
// lock before reading, as an append does. Without the lock a question that a
// turn wrote after the rewind had read the position, but before it deleted,
// escapes the delete and is left in a transcript the rewind was meant to cut
// back to the answer. Two transactions make that interleaving certain rather
// than a matter of timing. It needs real concurrency, so it runs only against
// PostgreSQL; SQLite serialises every writer and cannot show it.
func TestRewindHoldsTheConversationLockBeforeItReads(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, dbtest.Postgres(t, "a rewind racing a turn only interleaves on PostgreSQL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := group.NewStore(db).Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	account, err := user.NewStore(db).Create(ctx, nil, user.CreateInput{
		Username: "rewinder", PasswordHash: "not-a-real-hash",
	})
	if err != nil {
		t.Fatal(err)
	}

	store := NewStore(db)
	thread, err := store.Create(ctx, nil, account.ID, NewConversation{Title: "rewind"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID, UserID: account.ID, Role: RoleUser, Content: "question",
	}); err != nil {
		t.Fatal(err)
	}
	answer, err := store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID, UserID: account.ID, Role: RoleAssistant, Content: "answer",
	})
	if err != nil {
		t.Fatal(err)
	}

	// A turn writes its question and keeps its transaction open, as a turn does
	// between its writes and its commit.
	appended := make(chan Message, 1)
	commit := make(chan struct{})
	turnDone := make(chan error, 1)
	go func() {
		turnDone <- db.Tx(ctx, func(tx *database.Tx) error {
			message, err := store.Append(ctx, tx, AppendInput{
				ConversationID: thread.ID, UserID: account.ID, Role: RoleUser, Content: "late question",
			})
			if err != nil {
				return err
			}
			appended <- message
			<-commit
			return nil
		})
	}()

	var late Message
	select {
	case late = <-appended:
	case err := <-turnDone:
		t.Fatalf("the turn failed before it wrote its question: %v", err)
	}

	// The rewind starts from the answer after that question was written and
	// before it committed.
	rewound := make(chan error, 1)
	go func() {
		rewound <- db.Tx(ctx, func(tx *database.Tx) error {
			return store.TruncateFrom(ctx, tx, account.ID, thread.ID, answer.ID)
		})
	}()
	// The turn commits only once the rewind is blocked on its row. A fixed delay
	// would let a late rewind commit after the turn, and the test would pass
	// without the lock. The commit is released either way, so a rewind that never
	// blocks fails the test instead of leaving the turn open.
	blocked := awaitRewindLockWait(ctx, db)
	close(commit)
	if !blocked {
		t.Fatal("the rewind never waited on the conversation's lock")
	}

	if err := <-turnDone; err != nil {
		t.Fatalf("the turn: %v", err)
	}
	if err := <-rewound; err != nil {
		t.Fatalf("the rewind: %v", err)
	}

	// The rewind came after the question in the order the database kept, so
	// it cuts the question away with the answer.
	messages, err := store.Messages(ctx, nil, account.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.ID == late.ID {
			t.Fatalf("the rewind left a question it should have removed: %+v", messages)
		}
	}
}

// awaitRewindLockWait reports whether a session in this database is blocked on
// an UPDATE of conversations: the rewind, waiting for the turn's row. The
// database may be shared with other test packages, so a match is not proof on
// its own; the assertions after the commit decide whether the rewind was cut.
func awaitRewindLockWait(ctx context.Context, db *database.DB) bool {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		err := db.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock' AND query LIKE 'UPDATE conversations%'`).Scan(&waiting)
		if err == nil && waiting > 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
