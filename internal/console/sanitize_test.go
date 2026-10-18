package console

import (
	"bytes"
	"strings"
	"testing"
)

// Whatever comes out of a row is data, never control.
//
// A nickname has no character restrictions anywhere in this project — the
// length is capped and that is all — so `user show` on an account whose owner
// put an escape sequence in their own nickname was writing that sequence to
// an administrator's terminal, where it means what it says.
func TestRenderedDataCannotDriveTheTerminal(t *testing.T) {
	// A screen clear, a cursor home, and an OSC that sets the window title.
	hostile := "\x1b[2J\x1b[H\x1b]0;owned\x07alice"

	t.Run("table cells", func(t *testing.T) {
		var out bytes.Buffer
		RenderTable(&out, 100, true, []string{"username"}, [][]string{{hostile}})
		assertNoControlFromData(t, out.String())
		if !strings.Contains(out.String(), "alice") {
			t.Errorf("the harmless part of the value was dropped too:\n%q", out.String())
		}
	})

	t.Run("field values", func(t *testing.T) {
		var out bytes.Buffer
		RenderFields(&out, true, [][2]string{{"nickname", hostile}})
		assertNoControlFromData(t, out.String())
	})

	t.Run("field keys", func(t *testing.T) {
		var out bytes.Buffer
		RenderFields(&out, false, [][2]string{{hostile, "value"}})
		assertNoControlFromData(t, out.String())
	})
}

// The renderer's own SGR codes are allowed — they are the palette render.go
// documents. What must not appear is an escape that arrived in the data.
func assertNoControlFromData(t *testing.T, rendered string) {
	t.Helper()
	// Strip the codes this package legitimately emits, then nothing
	// escape-shaped may be left.
	for _, own := range []string{ansiReset, ansiBold, ansiDim, ansiUnderline, ansiRed} {
		rendered = strings.ReplaceAll(rendered, own, "")
	}
	if i := strings.IndexRune(rendered, 0x1b); i >= 0 {
		t.Errorf("an escape from the data survived at offset %d:\n%q", i, rendered)
	}
	for _, r := range rendered {
		if r != '\n' && isControl(r) {
			t.Errorf("control character %q survived in:\n%q", r, rendered)
		}
	}
}

// Dropping ESC leaves the rest of the sequence as visible text, which is the
// point: an operator sees junk in a nickname rather than the nickname quietly
// doing something.
func TestSanitizeLeavesTheSequenceVisible(t *testing.T) {
	if got := sanitize("\x1b[2Jalice"); got != "[2Jalice" {
		t.Errorf("sanitize = %q, want %q", got, "[2Jalice")
	}
	// The common case allocates nothing and returns the same string.
	if got := sanitize("alice"); got != "alice" {
		t.Errorf("an ordinary value was altered: %q", got)
	}
}

// Widths are measured after sanitizing, or a column is sized for characters
// that are never printed and the whole grid shears.
func TestTableColumnsAreSizedForWhatIsActuallyPrinted(t *testing.T) {
	var out bytes.Buffer
	RenderTable(&out, 100, false, []string{"a", "b"}, [][]string{
		{"\x1b[1;2;3;4;5;6;7;8;9m" + "x", "second"},
		{"y", "third"},
	})
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got:\n%q", out.String())
	}
	// Both data rows must start their second column at the same offset.
	first := strings.Index(lines[1], "second")
	second := strings.Index(lines[2], "third")
	if first != second {
		t.Errorf("columns did not line up: %d vs %d\n%s", first, second, out.String())
	}
}
