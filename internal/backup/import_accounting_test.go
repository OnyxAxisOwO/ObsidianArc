package backup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
)

// The character pre-check used to count what the file carried, untruncated. A
// message longer than the per-field limit is cut to that limit when it is
// written, so the ceiling must be measured against the cut text, or an import
// that fits is refused for characters it never stores.
func TestImportMeasuresTheTextItWritesNotTheTextItWasSent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	// Above the stored text of the message below, and below what the message
	// carries as sent.
	f.service.MaxStoredChars = int64(conversation.MaxContentChars + 3000)

	long := strings.Repeat("a", conversation.MaxContentChars+8000)
	document := Document{Format: Format, Conversations: []Thread{{
		Title: "t", Messages: []Turn{{Role: "user", Content: long}},
	}}}
	result, err := f.service.Import(ctx, f.account, document)
	if err != nil {
		t.Fatalf("an import that fits once cut to the field limit was refused: %v", err)
	}
	if result.Messages != 1 {
		t.Fatalf("wrote %d messages, want 1", result.Messages)
	}
}

// A pin that fails leaves the conversation in the account unpinned. The import
// still succeeds, and the result says how many were left that way, so the
// reader knows which conversations to look for.
func TestAPinThatFailsIsCountedInTheResult(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.service.pinConversation = func(context.Context, string, string) error {
		return errors.New("pin refused")
	}

	document := Document{Format: Format, Conversations: []Thread{{
		Title: "pinned", Pinned: true, Messages: []Turn{{Role: "user", Content: "hi"}},
	}}}
	result, err := f.service.Import(ctx, f.account, document)
	if err != nil {
		t.Fatalf("a failed pin failed the import: %v", err)
	}
	if result.Conversations != 1 || result.Unpinned != 1 {
		t.Fatalf("result = %+v, want the conversation written and counted as unpinned", result)
	}
}
