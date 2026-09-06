package text

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The whole reason this package exists: a byte-offset cut can land inside a
// multi-byte UTF-8 character and produce invalid bytes. Every case here must
// come back valid UTF-8, whatever the limit.
func TestTruncateAlwaysReturnsValidUTF8(t *testing.T) {
	cases := []struct {
		name  string
		value string
		limit int
	}{
		{"ascii only, well under the limit", "hello", 100},
		{"ascii only, cut mid-string", "hello world", 5},
		{"limit lands exactly on a rune boundary", "héllo", 2},
		// The shape from the bug report: 5 ASCII bytes then a 3-byte CJK
		// character then more data. A byte-offset cut at 6 lands on the
		// character's first byte (0xE4) and never reaches its other two —
		// exactly the "trailing 0xe4" corruption the report describes.
		{"limit falls inside a multi-byte character", strings.Repeat("M", 5) + "中" + strings.Repeat("N", 5), 6},
		{"limit keeps only the leading multi-byte character", "中hello", 1},
		{"multi-byte character exactly fills the limit", "中hello", 6},
		{"limit is zero", "hello", 0},
		{"value is empty", "", 5},
		{"limit exceeds the value's length", "中文", 50},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Truncate(c.value, c.limit)
			if !utf8.ValidString(got) {
				t.Fatalf("Truncate(%q, %d) = %q (% x), not valid UTF-8", c.value, c.limit, got, got)
			}
		})
	}
}

// Truncate's limit is a rune count, not a byte count: a string of entirely
// multi-byte characters must not be cut just because its byte length
// exceeds the limit.
func TestTruncateLimitCountsCharactersNotBytes(t *testing.T) {
	value := "你好世界文" // 5 runes, 15 bytes
	if got := Truncate(value, 5); got != value {
		t.Fatalf("Truncate(%q, 5) = %q, want the input untouched (5 runes fits a limit of 5)", value, got)
	}
	if got, want := Truncate(value, 3), "你好世"; got != want {
		t.Fatalf("Truncate(%q, 3) = %q, want %q (first 3 runes)", value, got, want)
	}
	// A byte-based cut at 5 would stop after "你" plus two more bytes,
	// slicing "好" in half. The rune-based cut takes 5 whole characters.
	if got := Truncate(value, 5); len([]rune(got)) != 5 {
		t.Fatalf("Truncate(%q, 5) kept %d runes, want 5", value, len([]rune(got)))
	}
}

// A value already inside the limit must come back exactly as given —
// Truncate is not a place for TrimAndTruncate's whitespace opinion to leak
// in by accident.
func TestTruncateLeavesInputInsideTheLimitUntouched(t *testing.T) {
	cases := []struct {
		value string
		limit int
	}{
		{"hello", 5},
		{"hello", 10},
		{"  padded  ", 20}, // Truncate must not trim; that is TrimAndTruncate's job.
		{"", 0},
		{"中文", 2},
	}
	for _, c := range cases {
		if got := Truncate(c.value, c.limit); got != c.value {
			t.Fatalf("Truncate(%q, %d) = %q, want the input unchanged", c.value, c.limit, got)
		}
	}
}

func TestTrimAndTruncateTrimsBeforeCounting(t *testing.T) {
	cases := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{"surrounding whitespace is removed", "  hello  ", 100, "hello"},
		{"the trim happens before the limit is applied", "  hello world  ", 5, "hello"},
		{"tabs and newlines count as whitespace too", "\t\nhello\n\t", 100, "hello"},
		{"a multi-byte character survives the trim and the cut", "  你好世界文  ", 3, "你好世"},
		{"whitespace-only input becomes empty", "   ", 5, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := TrimAndTruncate(c.value, c.limit)
			if got != c.want {
				t.Fatalf("TrimAndTruncate(%q, %d) = %q, want %q", c.value, c.limit, got, c.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("TrimAndTruncate(%q, %d) = %q, not valid UTF-8", c.value, c.limit, got)
			}
		})
	}
}
