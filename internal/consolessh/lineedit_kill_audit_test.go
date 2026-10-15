package consolessh

import (
	"strings"
	"testing"
)

// A paste past the bound is refused, and the refusal belongs to the line. A
// kill that empties the line takes that line with it, so the short command
// typed next runs. A kill that leaves text keeps the refusal: what was lost
// may still be in what is left.
func TestKillingTheOverlongLineClearsItsRefusal(t *testing.T) {
	overlong := strings.Repeat("a", maxLineRunes+1)
	cases := map[string]string{
		"Ctrl-U kills to the start": "\x15",
		"Ctrl-W deletes the word":   "\x17",
		"Ctrl-A then Ctrl-K":        "\x01\x0b",
	}
	for name, kill := range cases {
		t.Run(name, func(t *testing.T) {
			editor, client, _ := newPipeEditor(t, nil)
			line, err := drive(t, editor, client, overlong+kill+"ok\r")
			if err != nil {
				t.Fatalf("ReadLine: %v", err)
			}
			if line != "ok" {
				t.Fatalf("the short command after the kill ran as %q, want ok", line)
			}
		})
	}

	t.Run("a kill that leaves text keeps the refusal", func(t *testing.T) {
		editor, client, _ := newPipeEditor(t, nil)
		// Five runes left of the cursor survive Ctrl-U, so the line still holds
		// text and must stay refused on Enter.
		line, err := drive(t, editor, client, overlong+strings.Repeat("\x02", 5)+"\x15\r"+"ok\r")
		if err != nil {
			t.Fatalf("ReadLine: %v", err)
		}
		if line != "ok" {
			t.Fatalf("the truncated remainder ran as %q, want only the line after it", line)
		}
	})
}
