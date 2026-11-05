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
