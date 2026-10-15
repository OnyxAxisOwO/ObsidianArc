package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
)

// The archive panel walks the list a page at a time, so every archived
// conversation has to be reachable by advancing the offset. Without an offset
// the second page is the first again, and the rest of the archive is gone.
func TestArchivedListPagesThroughEveryConversation(t *testing.T) {
	f := newFixture(t)
	handlers := NewHandlers(f.service, f.conversations)
	mux := http.NewServeMux()
	handlers.Routes(mux)

	ctx := context.Background()
	archived := true
	const count = 5
	want := map[string]bool{}
	for i := 0; i < count; i++ {
		conv, err := f.conversations.Create(ctx, nil, f.account.ID,
			conversation.NewConversation{Title: fmt.Sprintf("Chat %d", i), ModelID: f.model.ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.conversations.Update(ctx, f.account.ID, conv.ID, conversation.Update{Archived: &archived}); err != nil {
			t.Fatal(err)
		}
		want[conv.ID] = true
	}

	seen := map[string]bool{}
	for offset := 0; ; offset += 2 {
		req := httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/api/conversations?archived=true&limit=2&offset=%d", offset), nil)
		req = req.WithContext(auth.WithUser(req.Context(), f.account))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("offset %d returned status %d: %s", offset, rec.Code, rec.Body.String())
		}

		var resp struct {
			Conversations []conversation.Conversation `json:"conversations"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Conversations) == 0 {
			break
		}
		for _, conv := range resp.Conversations {
			if seen[conv.ID] {
				t.Fatalf("conversation %s listed on more than one page", conv.ID)
			}
			seen[conv.ID] = true
		}
	}

	if len(seen) != count {
		t.Fatalf("pages listed %d archived conversations, want %d", len(seen), count)
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("archived conversation %s is unreachable by paging", id)
		}
	}
}
