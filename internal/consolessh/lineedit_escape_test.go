package consolessh

import (
	"testing"
)

// Keys with a parameter, such as PageUp, Insert and the modified arrows, used
// to leave their tail in the line: "ESC [ 5 ~" typed "~" and "ESC [ 1 ; 5 C"
// typed "5C". Each one must be consumed whole and do nothing.
func TestParameterisedSequencesAreConsumedWithoutTyping(t *testing.T) {
	cases := map[string]string{
		"PageUp":      "ab\x1b[5~c\r",
		"PageDown":    "ab\x1b[6~c\r",
		"Insert":      "ab\x1b[2~c\r",
		"Ctrl-Right":  "ab\x1b[1;5Cc\r",
		"Option-Left": "ab\x1b[1;3Dc\r",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			editor, client, _ := newPipeEditor(t, nil)
			line, err := drive(t, editor, client, input)
			if err != nil {
				t.Fatalf("ReadLine: %v", err)
			}
			if line != "abc" {
				t.Fatalf("got %q, want %q", line, "abc")
			}
		})
	}
}

// Home and End arrive as ESC [ 7 ~ and ESC [ 8 ~ from rxvt-style terminals.
func TestRxvtHomeAndEndMoveTheCursor(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	line, err := drive(t, editor, client, "bc\x1b[7~a\x1b[8~d\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "abcd" {
		t.Fatalf("got %q, want %q", line, "abcd")
	}
}

// Up arrow during reverse search used to cancel the search and then type "[A".
// The Esc still cancels, and the rest of the arrow key is swallowed, so the
// draft comes back intact and nothing is inserted.
func TestArrowKeyDuringReverseSearchIsNotTyped(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	if _, err := drive(t, editor, client, "user list\r"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	line, err := drive(t, editor, client, "dra\x12user\x1b[Aft\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "draft" {
		t.Fatalf("got %q, want %q: the arrow key was typed into the line", line, "draft")
	}
}

// A lone Esc during search still cancels, and the key after it is an ordinary
// key rather than being eaten as the tail of a sequence.
func TestLoneEscDuringReverseSearchTypesTheNextKey(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	if _, err := drive(t, editor, client, "user list\r"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	line, err := drive(t, editor, client, "dra\x12user\x1bx\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "drax" {
		t.Fatalf("got %q, want %q", line, "drax")
	}
}

// The Esc that cancels a search can be followed by typed text that starts like
// an escape sequence. An 'O' or '[' is not a sequence unless the bytes after
// it are one, so the letters are typed. Nothing is lost.
func TestTypedTextAfterSearchEscIsNotEatenAsASequence(t *testing.T) {
	cases := map[string]struct{ input, want string }{
		"capital O word": {"dra\x12user\x1bObvious\r", "draObvious"},
		"bracket then b": {"dra\x12user\x1b[bc\r", "dra[bc"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			editor, client, _ := newPipeEditor(t, nil)
			if _, err := drive(t, editor, client, "user list\r"); err != nil {
				t.Fatalf("seed: %v", err)
			}
			line, err := drive(t, editor, client, tc.input)
			if err != nil {
				t.Fatalf("ReadLine: %v", err)
			}
			if line != tc.want {
				t.Fatalf("got %q, want %q", line, tc.want)
			}
		})
	}
}

// An Esc that begins a new sequence after a search-cancelling Esc must not
// inherit the search's swallowing. Otherwise the arrow that follows is dropped,
// and here the Left arrow would move the cursor and the x would land before the a.
func TestNewEscAfterSearchEscDoesNotSwallowTheArrowThatFollows(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	if _, err := drive(t, editor, client, "user list\r"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	line, err := drive(t, editor, client, "dra\x12user\x1b[\x1b[Dx\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "drxa" {
		t.Fatalf("got %q, want %q: the Left arrow was dropped", line, "drxa")
	}
}
