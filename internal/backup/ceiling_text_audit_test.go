package backup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
)

// The character ceiling is the figure the disk feels, so it must count every
// text column an import writes. Error and title were written without being
// counted, which let a document of error-only turns (no content, no provider
// call) fill an account past the ceiling.
func TestImportCountsErrorTextAgainstTheCeiling(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.service.MaxStoredChars = 500

	document := Document{Format: Format, Conversations: []Thread{{
		Title: "t",
		Messages: []Turn{
			{Role: "assistant", Error: strings.Repeat("e", 300)},
			{Role: "assistant", Error: strings.Repeat("e", 300)},
		},
	}}}
	if _, err := f.service.Import(ctx, f.account, document); !errors.Is(err, ErrStorageFull) {
		t.Fatalf("600 characters of error text against a 500-character ceiling gave %v, want ErrStorageFull", err)
	}
}

func TestImportCountsTitlesAgainstTheCeiling(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.service.MaxStoredChars = 150

	document := Document{Format: Format, Conversations: []Thread{{
		Title:    strings.Repeat("T", 200),
		Messages: []Turn{{Role: "user", Content: "hi"}},
	}}}
	if _, err := f.service.Import(ctx, f.account, document); !errors.Is(err, ErrStorageFull) {
		t.Fatalf("a 200-character title against a 150-character ceiling gave %v, want ErrStorageFull", err)
	}
}

// What the account already holds counts too: an error stored by the chat path
// is text the ceiling is about, whichever path wrote it.
func TestStoredErrorTextCountsAgainstTheCeiling(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.service.MaxStoredChars = 400

	created, err := f.conversations.Create(ctx, nil, f.account.ID, conversation.NewConversation{Title: "held"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.conversations.Append(ctx, nil, conversation.AppendInput{
		ConversationID: created.ID, UserID: f.account.ID, Role: conversation.RoleAssistant,
		Error: strings.Repeat("e", 500),
	}); err != nil {
		t.Fatal(err)
	}

	document := Document{Format: Format, Conversations: []Thread{{
		Messages: []Turn{{Role: "user", Content: "a"}},
	}}}
	if _, err := f.service.Import(ctx, f.account, document); !errors.Is(err, ErrStorageFull) {
		t.Fatalf("500 stored error characters against a 400-character ceiling gave %v, want ErrStorageFull", err)
	}
}
