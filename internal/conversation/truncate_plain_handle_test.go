package conversation

import (
	"context"
	"testing"
)

// A rewind handed no transaction runs its own, so its lock holds across the
// position read and the delete. Both must still happen on either engine.
func TestTruncateFromWithoutATransactionRemovesTheSuffix(t *testing.T) {
	ctx := context.Background()
	store, account := nulFixture(t)

	thread, err := store.Create(ctx, nil, account.ID, NewConversation{Title: "rewind"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID, UserID: account.ID, Role: RoleUser, Content: "question",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID, UserID: account.ID, Role: RoleAssistant, Content: "answer",
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.TruncateFrom(ctx, nil, account.ID, thread.ID, first.ID); err != nil {
		t.Fatalf("truncate without a transaction: %v", err)
	}
	messages, err := store.Messages(ctx, nil, account.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("truncate from the first message left %d messages", len(messages))
	}
}
