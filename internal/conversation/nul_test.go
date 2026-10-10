package conversation

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The title and message writers run on both engines: PostgreSQL refuses a text
// value that holds a NUL byte and fails the statement carrying it, which is how
// a chat turn with one in it used to be lost, while SQLite keeps it.
func nulFixture(t *testing.T) (*Store, user.User) {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, dbtest.Either(t, filepath.Join(t.TempDir(), "nul.db")))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := group.NewStore(db).Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	account, err := user.NewStore(db).Create(ctx, nil, user.CreateInput{
		Username: "writer", PasswordHash: "not-a-real-hash",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return NewStore(db), account
}

func TestNULBytesAreRemovedBeforeAnyTitleOrMessageIsStored(t *testing.T) {
	ctx := context.Background()
	store, account := nulFixture(t)

	thread, err := store.Create(ctx, nil, account.ID, NewConversation{Title: "ti\x00tle"})
	if err != nil {
		t.Fatalf("create with a NUL in the title: %v", err)
	}
	question, err := store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID, UserID: account.ID, Role: RoleUser, Content: "ask\x00 here",
	})
	if err != nil {
		t.Fatalf("append a user message with a NUL: %v", err)
	}
	if _, err := store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID, UserID: account.ID, Role: RoleAssistant,
		Content: "an\x00swer", Reasoning: "thi\x00nk", Error: "pro\x00vider",
	}); err != nil {
		t.Fatalf("append an assistant message with a NUL: %v", err)
	}

	renamed := "re\x00named"
	if _, err := store.Update(ctx, account.ID, thread.ID, Update{Title: &renamed}); err != nil {
		t.Fatalf("rename with a NUL: %v", err)
	}
	if _, err := store.UpdateMessage(ctx, nil, account.ID, thread.ID, question.ID, "ed\x00ited"); err != nil {
		t.Fatalf("edit with a NUL: %v", err)
	}

	untitled, err := store.Create(ctx, nil, account.ID, NewConversation{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTitle(ctx, nil, account.ID, untitled.ID, "de\x00rived"); err != nil {
		t.Fatalf("set the first-turn title with a NUL: %v", err)
	}

	got, err := store.Get(ctx, nil, account.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "renamed" {
		t.Errorf("title = %q, want the NUL removed", got.Title)
	}
	derived, err := store.Get(ctx, nil, account.ID, untitled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if derived.Title != "derived" {
		t.Errorf("first-turn title = %q, want the NUL removed", derived.Title)
	}

	messages, err := store.Messages(ctx, nil, account.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("stored %d messages, want 2", len(messages))
	}
	user, assistant := messages[0], messages[1]
	if user.Content != "edited" {
		t.Errorf("edited content = %q, want the NUL removed", user.Content)
	}
	if assistant.Content != "answer" || assistant.Reasoning != "think" || assistant.Error != "provider" {
		t.Errorf("assistant fields = %q / %q / %q, want each with its NUL removed",
			assistant.Content, assistant.Reasoning, assistant.Error)
	}
}
