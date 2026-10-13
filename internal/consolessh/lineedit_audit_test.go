package consolessh

import (
	"strings"
	"testing"
)

// The line length bound is the editor's answer to a peer that pastes without
// end. Reverse search takes its own term, and it was not bounded: each rune of
// the term was copied and redrawn on every keystroke, and the term grew for as
// long as the peer kept typing.
func TestReverseSearchTermIsBoundedLikeTheLine(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	if _, err := drive(t, editor, client, "user list\r"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Ctrl-R, a term longer than any search may be, Ctrl-G to leave search, then
	// Enter to submit what the line holds.
	input := "\x12" + strings.Repeat("u", maxLineRunes+300) + "\x07\r"
	if _, err := drive(t, editor, client, input); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if got := len(editor.searchTerm); got > maxSearchRunes {
		t.Fatalf("search term grew to %d runes, want at most %d", got, maxSearchRunes)
	}
}
