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

// A lone Esc followed by a letter used to lose the letter, and Esc then Enter
// lost the Enter, so Alt-Enter could not submit. The Esc is dropped on its own
// and the key after it is an ordinary key.
func TestLoneEscDropsOnlyTheEscape(t *testing.T) {
	cases := map[string]struct{ input, want string }{
		"Esc then letter": {"ab\x1bx\r", "abx"},
		"Esc then Enter":  {"ab\x1b\r", "ab"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			editor, client, _ := newPipeEditor(t, nil)
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

// A paste past the line bound used to run its first maxLineRunes runes and drop
// the rest with no word about it, so a command with a trailing --yes or a value
// at the end could run cut short. The line is refused whole instead: the notice
// says so, nothing of it runs, and the line typed after it does.
func TestOverlongLineIsNotRun(t *testing.T) {
	editor, client, out := newPipeEditor(t, nil)
	line, err := drive(t, editor, client, strings.Repeat("a", maxLineRunes+100)+"TAIL\r"+"ok\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "ok" {
		t.Fatalf("the overlong line ran as %q, want only the line after it", line)
	}
	if n := strings.Count(out.String(), "[input dropped"); n != 1 {
		t.Fatalf("the refusal was announced %d times, want once", n)
	}
	if n := strings.Count(out.String(), "[line not run"); n != 1 {
		t.Fatalf("the line was not reported as not run %d times, want once", n)
	}

	t.Run("a line exactly at the bound still runs", func(t *testing.T) {
		editor, client, _ := newPipeEditor(t, nil)
		line, err := drive(t, editor, client, strings.Repeat("a", maxLineRunes)+"\r")
		if err != nil {
			t.Fatalf("ReadLine: %v", err)
		}
		if len([]rune(line)) != maxLineRunes {
			t.Fatalf("line holds %d runes, want all %d", len([]rune(line)), maxLineRunes)
		}
	})

	t.Run("backspacing back under the bound does not make it runnable", func(t *testing.T) {
		editor, client, _ := newPipeEditor(t, nil)
		line, err := drive(t, editor, client, strings.Repeat("a", maxLineRunes+1)+"\x7f\r"+"ok\r")
		if err != nil {
			t.Fatalf("ReadLine: %v", err)
		}
		if line != "ok" {
			t.Fatalf("the refused line ran after a backspace as %q, want only the line after it", line)
		}
	})
}

// Up saves the line being typed as the draft and Down restores it. A draft that
// lost runes to the bound must come back refused, or the kept prefix runs after
// the trip through history.
func TestOverlongDraftStaysRefusedAcrossHistory(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	if _, err := drive(t, editor, client, "user list\r"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Ctrl-P is Up, Ctrl-N is Down; the draft is restored by the Down that
	// steps past the newest history line.
	line, err := drive(t, editor, client, strings.Repeat("a", maxLineRunes+1)+"\x10\x0e\r"+"ok\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "ok" {
		t.Fatalf("the overlong draft ran after a trip through history as %q, want only the line after it", line)
	}
}
