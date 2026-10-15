package conversation

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The lock names the owner in its WHERE clause, but nothing checked that the
// lock matched a row, so a caller that named someone else's conversation got
// its message written into that thread under the caller's own account.
func TestAppendIntoAnotherUsersConversationIsNotFound(t *testing.T) {
	ctx := context.Background()
	store, users, owner := attachmentFixture(t)
	intruder, err := users.Create(ctx, nil, user.CreateInput{
		Username: "intruder", PasswordHash: "not-a-real-hash",
	})
	if err != nil {
		t.Fatalf("create second user: %v", err)
	}
	thread, err := store.Create(ctx, nil, owner.ID, NewConversation{Title: "mine"})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	input := AppendInput{
		ConversationID: thread.ID,
		UserID:         intruder.ID,
		Role:           RoleUser,
		Content:        "planted",
	}
	if _, err := store.Append(ctx, nil, input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("append without a transaction = %v, want ErrNotFound", err)
	}
	if err := store.db.Tx(ctx, func(tx *database.Tx) error {
		_, err := store.Append(ctx, tx, input)
		return err
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("append inside a transaction = %v, want ErrNotFound", err)
	}

	// Messages is scoped to the owner, so count the thread's rows directly to
	// see whether the intruder's message was written into it.
	var rows int
	if err := store.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM messages WHERE conversation_id = ?`, thread.ID).Scan(&rows); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if rows != 0 {
		t.Fatalf("the owner's conversation gained %d messages", rows)
	}
}
