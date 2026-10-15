package conversation

import (
	"context"
	"fmt"
	"testing"
)

func TestConversationPagesHaveStableOrderingAndOwnerTotals(t *testing.T) {
	store, _, account := attachmentFixture(t)
	ctx := context.Background()
	for i := 0; i < 65; i++ {
		if _, err := store.db.Exec(ctx, `INSERT INTO conversations (id, user_id, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, fmt.Sprintf("c-%02d", i), account.ID, "Title", 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for offset := 0; offset < 65; offset += 20 {
		rows, total, err := store.ListPage(ctx, account.ID, 20, offset)
		if err != nil || total != 65 {
			t.Fatalf("conversation page: total %d, %v", total, err)
		}
		for _, row := range rows {
			if seen[row.ID] {
				t.Fatalf("duplicate %s", row.ID)
			}
			seen[row.ID] = true
		}
	}
	if len(seen) != 65 {
		t.Fatalf("only reached %d conversations", len(seen))
	}
	rows, total, err := store.ListPage(ctx, "someone-else", 20, 0)
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("another owner's conversations: %d %d %v", len(rows), total, err)
	}
}

// The total has to count what the pages list. An archived conversation is not
// on any page, so counting it made the total larger than the pages could ever
// reach.
func TestConversationTotalExcludesArchivedConversations(t *testing.T) {
	store, _, account := attachmentFixture(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		archived := i >= 3
		if _, err := store.db.Exec(ctx, `INSERT INTO conversations (id, user_id, title, archived, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, fmt.Sprintf("a-%02d", i), account.ID, "Title", archived, 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	listed := 0
	for offset := 0; ; offset += 2 {
		rows, total, err := store.ListPage(ctx, account.ID, 2, offset)
		if err != nil {
			t.Fatal(err)
		}
		if total != 3 {
			t.Fatalf("total %d, want the 3 active conversations", total)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if row.Archived {
				t.Fatalf("archived conversation %s on an active page", row.ID)
			}
		}
		listed += len(rows)
	}
	if listed != 3 {
		t.Fatalf("pages listed %d conversations, want 3", listed)
	}
}
