package consolessh

import (
	"strings"
	"testing"
)

// Len is how many bytes the editor has written, which is what the output tests
// count.
func (s *syncBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}

// Typing or pasting at the end of the line wrote the whole line, prompt and
// all, after every rune: a paste of n runes wrote about n squared over two.
// At the end of the line the rune is all that changes on the screen.
func TestPasteAtTheEndOfTheLineWritesLinearly(t *testing.T) {
	editor, client, out := newPipeEditor(t, nil)
	const runes = 4096
	if _, err := drive(t, editor, client, strings.Repeat("a", runes)+"\r"); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if written := out.Len(); written > 4*runes+1024 {
		t.Fatalf("a %d-rune paste wrote %d bytes; want about one per rune", runes, written)
	}
}

// Cursor moves wrote the whole line too, so an arrow key the size of three
// bytes produced a line-length reply. A move needs only the cursor movement.
func TestArrowKeysMoveTheCursorWithoutRedrawingTheLine(t *testing.T) {
	editor, client, out := newPipeEditor(t, nil)
	const typed, arrows = 2000, 500
	input := strings.Repeat("a", typed) + strings.Repeat("\x1b[D", arrows) + "\r"
	if _, err := drive(t, editor, client, input); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if written := out.Len(); written > typed+arrows*8+1024 {
		t.Fatalf("%d arrow keys after %d runes wrote %d bytes; a move should not rewrite the line", arrows, typed, written)
	}
}

// An insertion in the middle of the line writes the rune and the text after it,
// then walks the cursor back over that text, and nothing else.
func TestMidLineInsertWritesOnlyTheTail(t *testing.T) {
	editor, client, out := newPipeEditor(t, nil)
	line, err := drive(t, editor, client, "ac\x1b[Db\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "abc" {
		t.Fatalf("got %q, want %q", line, "abc")
	}
	if !strings.Contains(out.String(), "bc\x1b[1D") {
		t.Fatalf("output %q does not write the inserted rune and its tail, then walk back over the tail", out.String())
	}
}
