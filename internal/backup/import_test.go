package backup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
)

// SQLite's LENGTH stops counting at the first NUL, so a message whose text
// begins with one counts as nothing when the ceiling is measured in SQL. An
// account could then store far past its ceiling behind a single character.
func TestStoredTextAfterANULStillCountsAgainstTheCeiling(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	f.service.MaxStoredChars = 10_000
	conversationID := f.write(t, f.account, "holds a NUL")
	// Written through the store, as the chat path writes it: nothing there
	// refuses a NUL, so an account can already hold one.
	if _, err := f.conversations.Append(ctx, nil, conversation.AppendInput{
		ConversationID: conversationID, UserID: f.account.ID,
		Role: conversation.RoleUser, Content: "\x00" + strings.Repeat("x", 9000),
	}); err != nil {
		t.Fatal(err)
	}

	// 9001 stored plus 3000 incoming is over the 10000 ceiling. Measured with
	// LENGTH the account would look empty and this would be accepted.
	document := Document{Format: Format, Conversations: []Thread{{
		Title:    "more",
		Messages: []Turn{{Role: "user", Content: strings.Repeat("y", 3000)}},
	}}}
	if _, err := f.service.Import(ctx, f.account, document); !errors.Is(err, ErrStorageFull) {
		t.Fatalf("import past the ceiling gave %v, want ErrStorageFull", err)
	}
}

// A NUL is refused wherever the import would store text, and before anything
// is written. Dropping the character would store something other than what
// the file says, in every message it appears in.
func TestImportRefusesNULInAnyTextItWouldStore(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	const withNUL = "a\x00b"
	cases := []struct {
		name     string
		document Document
	}{
		{"title", Document{Format: Format, Conversations: []Thread{{
			Title: withNUL, Messages: []Turn{{Role: "user", Content: "x"}},
		}}}},
		{"role", Document{Format: Format, Conversations: []Thread{{
			Title: "t", Messages: []Turn{{Role: "user\x00", Content: "x"}},
		}}}},
		{"content", Document{Format: Format, Conversations: []Thread{{
			Title: "t", Messages: []Turn{{Role: "user", Content: withNUL}},
		}}}},
		{"reasoning", Document{Format: Format, Conversations: []Thread{{
			Title: "t", Messages: []Turn{{Role: "assistant", Content: "x", Reasoning: withNUL}},
		}}}},
		{"error", Document{Format: Format, Conversations: []Thread{{
			Title: "t", Messages: []Turn{{Role: "assistant", Error: withNUL}},
		}}}},
		{"model name", Document{Format: Format, Conversations: []Thread{{
			Title: "t", Messages: []Turn{{Role: "assistant", Content: "x", ModelName: withNUL}},
		}}}},
	}
	for _, tc := range cases {
		if _, err := f.service.Import(ctx, f.account, tc.document); !errors.Is(err, ErrNULCharacter) {
			t.Errorf("%s: gave %v, want ErrNULCharacter", tc.name, err)
		}
	}

	if n, _ := f.conversations.CountMessages(ctx, nil, f.account.ID); n != 0 {
		t.Errorf("refused imports wrote %d messages", n)
	}
	threads, err := f.conversations.List(ctx, f.account.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 0 {
		t.Errorf("refused imports left %d conversations behind", len(threads))
	}
}

// The text ceiling is in characters, which is how it is measured everywhere
// else. The check ahead of an import counted bytes, so a CJK document (three
// bytes a character) was refused well before it reached the ceiling.
func TestTextCeilingCountsCharactersNotBytes(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	f.service.MaxStoredChars = 10_000
	document := func(characters int) Document {
		return Document{Format: Format, Conversations: []Thread{{
			Title:    "t",
			Messages: []Turn{{Role: "user", Content: strings.Repeat("字", characters)}},
		}}}
	}

	// 4000 characters are 12000 bytes: over the ceiling by bytes, under it by
	// characters.
	if _, err := f.service.Import(ctx, f.account, document(4000)); err != nil {
		t.Fatalf("4000 characters against a 10000-character ceiling: %v", err)
	}
	if _, err := f.service.Import(ctx, f.account, document(6001)); !errors.Is(err, ErrStorageFull) {
		t.Fatalf("4000 stored plus 6001 incoming gave %v, want ErrStorageFull", err)
	}
}
