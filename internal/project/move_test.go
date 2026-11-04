package project

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
)

// Filing a conversation is the rail's drag: into one of the owner's
// projects, or back out of every project. The project is checked in the same
// statement that files it, so a project that is somebody else's, or that is
// gone, is refused rather than written.
func TestAConversationMovesOnlyIntoItsOwnersProjects(t *testing.T) {
	ctx := context.Background()
	projects, owner, stranger := fixture(t)
	conversations := conversation.NewStore(projects.db)

	mine, err := projects.Create(ctx, owner.ID, "Mine", "")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := projects.Create(ctx, stranger.ID, "Theirs", "")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := conversations.Create(ctx, nil, owner.ID, conversation.NewConversation{Title: "Loose"})
	if err != nil {
		t.Fatal(err)
	}
	move := func(projectID string) (conversation.Conversation, error) {
		return conversations.Update(ctx, owner.ID, chat.ID, conversation.Update{ProjectID: &projectID})
	}

	filed, err := move(mine.ID)
	if err != nil || filed.ProjectID != mine.ID {
		t.Fatalf("into own project: %+v, %v", filed, err)
	}
	if filed.UpdatedAt != chat.UpdatedAt {
		t.Errorf("filing reordered the rail: updated_at %d, was %d", filed.UpdatedAt, chat.UpdatedAt)
	}

	if _, err := move(theirs.ID); !errors.Is(err, conversation.ErrProjectNotFound) {
		t.Fatalf("into somebody else's project: %v, want ErrProjectNotFound", err)
	}
	if got, _ := conversations.Get(ctx, nil, owner.ID, chat.ID); got.ProjectID != mine.ID {
		t.Fatalf("a refused move still moved it: project %q", got.ProjectID)
	}

	if err := projects.Delete(ctx, owner.ID, mine.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := move(mine.ID); !errors.Is(err, conversation.ErrProjectNotFound) {
		t.Fatalf("into a deleted project: %v, want ErrProjectNotFound", err)
	}

	again, err := projects.Create(ctx, owner.ID, "Again", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := move(again.ID); err != nil {
		t.Fatal(err)
	}
	out, err := move("")
	if err != nil || out.ProjectID != "" {
		t.Fatalf("out of every project: %+v, %v", out, err)
	}

	// Somebody else's conversation is not found, whatever the project.
	other := ""
	if _, err := conversations.Update(ctx, stranger.ID, chat.ID, conversation.Update{ProjectID: &other}); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("moving somebody else's conversation: %v, want ErrNotFound", err)
	}
}
