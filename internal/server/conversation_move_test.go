package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/project"
)

// The rail's mocked API cannot prove the handler reads project_id or keeps
// account boundaries; exercise the same PATCH against a whole server here.
func TestConversationFilingOverHTTP(t *testing.T) {
	in := newInstance(t)
	owner := in.register("owner", "a-good-password")
	stranger := in.register("stranger", "a-good-password")
	ctx := context.Background()
	folders := project.NewStore(in.db)
	mine, err := folders.Create(ctx, owner.userID, "Mine", "")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := folders.Create(ctx, stranger.userID, "Theirs", "")
	if err != nil {
		t.Fatal(err)
	}
	chats := conversation.NewStore(in.db)
	chat, err := chats.Create(ctx, nil, owner.userID, conversation.NewConversation{Title: "Loose"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/conversations/" + chat.ID
	patch := func(projectID string, as *session, want int) {
		t.Helper()
		res := in.do(http.MethodPatch, path, map[string]string{"project_id": projectID}, as)
		if res.Code != want {
			t.Fatalf("PATCH project_id=%q: %d %s, want %d", projectID, res.Code, res.Body.String(), want)
		}
	}
	patch(mine.ID, nil, http.StatusUnauthorized)
	patch(mine.ID, owner, http.StatusOK)
	patch("malformed", owner, http.StatusBadRequest)
	patch(theirs.ID, owner, http.StatusNotFound)
	patch(id.New(), owner, http.StatusNotFound)
	patch("", stranger, http.StatusNotFound)
	got, err := chats.Get(ctx, nil, owner.userID, chat.ID)
	if err != nil || got.ProjectID != mine.ID || got.UpdatedAt != chat.UpdatedAt {
		t.Fatalf("refused moves changed filing or order: %+v, %v", got, err)
	}
	loose := decode[struct {
		Conversations []conversation.Conversation `json:"conversations"`
	}](t, in.do(http.MethodGet, "/api/conversations?project_id=", nil, owner))
	if len(loose.Conversations) != 0 {
		t.Fatalf("filed chat appears in the loose list: %+v", loose.Conversations)
	}
	patch("", owner, http.StatusOK)
	loose = decode[struct {
		Conversations []conversation.Conversation `json:"conversations"`
	}](t, in.do(http.MethodGet, "/api/conversations?project_id=", nil, owner))
	if len(loose.Conversations) != 1 || loose.Conversations[0].ID != chat.ID || loose.Conversations[0].ProjectID != "" {
		t.Fatalf("moving out did not reach the loose list: %+v", loose.Conversations)
	}
}
