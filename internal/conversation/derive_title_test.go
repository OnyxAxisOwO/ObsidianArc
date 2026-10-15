package conversation

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

// The title is the first sixty characters of the message once its spacing is
// collapsed. Whitespace the scan drops must not count toward the window, or a
// message that opens with a long run of newlines is titled from the run.
func TestDeriveTitleSkipsLeadingWhitespaceBeforeCounting(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"a run of newlines before the words", strings.Repeat("\n", 1500) + "Hello world", "Hello world"},
		{"a run of spaces inside the opening", "a" + strings.Repeat(" ", 2000) + "b", "a b"},
		{"ideographic spaces count as whitespace", strings.Repeat("　", 1500) + "你好", "你好"},
		{"blank input has no title", strings.Repeat("\n", 1500), ""},
		{"opening is trimmed and collapsed", "  Hello \t\n  world  ", "Hello world"},
		{"a long opening is cut at sixty characters", strings.Repeat("中", 100), strings.Repeat("中", 60)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DeriveTitle(c.content); got != c.want {
				t.Fatalf("DeriveTitle(%d bytes) = %q, want %q", len(c.content), got, c.want)
			}
		})
	}
}

// The bounded scan must agree with the whole-message computation it replaces,
// including on bytes that are not UTF-8, NUL and the ideographic space.
func TestDeriveTitleMatchesWholeMessageCollapse(t *testing.T) {
	whole := func(content string) string {
		return text.TrimAndTruncate(strings.Join(strings.Fields(content), " "), 60)
	}
	alphabet := []string{"a", "b", " ", "\n", "\t", "　", "中", "\x00", "\xff", "é", "x"}
	random := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		var b strings.Builder
		for n := random.Intn(400); n > 0; n-- {
			b.WriteString(alphabet[random.Intn(len(alphabet))])
		}
		content := b.String()
		if got, want := DeriveTitle(content), whole(content); got != want {
			t.Fatalf("DeriveTitle(%q) = %q, want %q", content, got, want)
		}
	}
}
