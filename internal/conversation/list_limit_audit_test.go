package conversation

import (
	"context"
	"fmt"
	"testing"
)

// A client-supplied limit above the ceiling falls back to the default, as it
// does on ListPage. ListWithFilter, which the chat list calls with the query
// string's value, returned every row it was asked for.
func TestListWithFilterCapsTheLimitLikeEveryOtherListing(t *testing.T) {
	store, _, account := attachmentFixture(t)
	ctx := context.Background()
	for i := 0; i < MaxListLimit+1; i++ {
		if _, err := store.db.Exec(ctx, `INSERT INTO conversations (id, user_id, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, fmt.Sprintf("c-%04d", i), account.ID, "Title", 1, 1); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := store.ListWithFilter(ctx, account.ID, ListFilter{Limit: 100000000})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != DefaultListLimit {
		t.Fatalf("a limit of 100000000 returned %d rows, want the default %d", len(rows), DefaultListLimit)
	}
}
