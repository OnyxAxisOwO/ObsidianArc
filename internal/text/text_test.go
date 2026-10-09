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

// A limit that is not positive keeps nothing rather than panicking. The
// guard exists because runes[:limit] is a runtime panic for a negative limit,
// and a bounding helper that crashes on unexpected input is worse than one
// that returns something too short: the caller that "knew" its limit was
// positive is exactly the caller that never tested the other case.
func TestTruncateWithANonPositiveLimitKeepsNothing(t *testing.T) {
	for _, limit := range []int{0, -1, -80} {
		for _, value := range []string{"", "hello", "你好世界文"} {
			got := Truncate(value, limit)
			if got != "" {
				t.Errorf("Truncate(%q, %d) = %q, want empty", value, limit, got)
			}
		}
	}

	// TrimAndTruncate goes through the same guard, and it must not be the
	// trim that saves it: the limit is what is being tested.
	if got := TrimAndTruncate("  hello  ", -5); got != "" {
		t.Errorf("TrimAndTruncate(%q, %d) = %q, want empty", "  hello  ", -5, got)
	}
}

// Clean is what lets a client-supplied string reach PostgreSQL. Each case
// states the bytes a client could send and what has to be stored instead.
func TestCleanMakesAStringStorableByPostgres(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"plain ASCII is left alone", "/api/chat", "/api/chat"},
		{"valid multi-byte text is left alone", "中文 café 🙂", "中文 café 🙂"},
		{"empty stays empty", "", ""},
		{"tabs and newlines are kept", "a\tb\nc", "a\tb\nc"},
		{"a NUL byte is removed", "/a\x00b", "/ab"},
		{"a lone invalid byte becomes U+FFFD", "a\xffb", "a\uFFFDb"},
		{"a run of invalid bytes becomes one U+FFFD", "a\xff\xfeb", "a\uFFFDb"},
		{"a truncated multi-byte sequence becomes U+FFFD", "a\xe4\xb8b", "a\uFFFDb"},
		{"a surrogate code point encoded as UTF-8 is invalid", "a\xed\xa0\x80b", "a\uFFFDb"},
		{"NUL and invalid bytes together", "\x00\xff\x00", "\uFFFD"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Clean(c.value)
			if got != c.want {
				t.Fatalf("Clean(%q) = %q, want %q", c.value, got, c.want)
			}
			if !utf8.ValidString(got) || strings.ContainsRune(got, 0) {
				t.Fatalf("Clean(%q) = %q, still holds invalid UTF-8 or a NUL byte", c.value, got)
			}
		})
	}
}

// Whatever two bytes sit between two ASCII letters, the result must be
// storable. The space is 65,536 values, so it is simply all of them.
func TestCleanLeavesNoBadBytesForAnyBytePair(t *testing.T) {
	for first := 0; first < 256; first++ {
		for second := 0; second < 256; second++ {
			value := "a" + string([]byte{byte(first), byte(second)}) + "b"
			got := Clean(value)
			if !utf8.ValidString(got) || strings.ContainsRune(got, 0) {
				t.Fatalf("Clean(% x) = %q, still holds invalid UTF-8 or a NUL byte", value, got)
			}
		}
	}
}
