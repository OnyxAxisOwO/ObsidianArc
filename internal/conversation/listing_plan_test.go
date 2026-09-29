package conversation

import (
	"context"
	"strings"
	"testing"
)

// The rail's list is read after every turn, so how the engine answers it is
// worth pinning down: from ix_conversations_archived_listing, in index order,
// with no sort of the account's history in between. SQLite's plan is the one
// this test can read without a server; the index is the same on Postgres.
func TestTheRailListIsAnsweredFromItsIndexWithoutASort(t *testing.T) {
	store, _, account := attachmentFixture(t)
	ctx := context.Background()

	rows, err := store.db.Query(ctx,
		`EXPLAIN QUERY PLAN SELECT `+conversationColumns+` FROM conversations
		 WHERE user_id = ? AND archived = ?
		 ORDER BY pinned DESC, updated_at DESC, id DESC LIMIT ? OFFSET ?`,
		account.ID, false, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	text := strings.Join(plan, "\n")
	if !strings.Contains(text, "ix_conversations_archived_listing") {
		t.Errorf("the list does not use its index:\n%s", text)
	}
	if strings.Contains(text, "TEMP B-TREE") {
		t.Errorf("the list sorts instead of reading in index order:\n%s", text)
	}
}
