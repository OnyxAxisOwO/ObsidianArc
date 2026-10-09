// Package text holds the string-bounding helper that every package storing a
// client- or user-supplied string needs: cut it to a maximum length without
// producing invalid UTF-8. It used to be copied six times, under three
// names, in six packages — and one copy drifted. It sliced by byte offset
// instead of by rune, so a multi-byte UTF-8 character sitting on the cut
// point got sliced in half. SQLite stores the resulting bytes without
// complaint; PostgreSQL rejects the insert outright with "invalid byte
// sequence for encoding UTF8", so the same code path worked in development
// and turned sign-in into a 500 in production. Six call sites is what
// justifies a package for one function: it puts the rune-safety in one place
// a reader can audit, instead of trusting six copies to agree forever.
package text

import "strings"

// Truncate returns value cut to at most limit runes. It slices by rune, not
// by byte, so a multi-byte UTF-8 character at the cut point is kept whole or
// dropped whole — it is never split into a dangling, invalid byte sequence.
//
// Use this for a value that must be kept verbatim — a request header, a log
// line — where surrounding whitespace is part of what was actually sent and
// is not this function's business to remove.
//
// A value already inside the limit comes back exactly as given, NUL bytes and
// invalid UTF-8 included. A value headed for PostgreSQL goes through Clean
// first.
func Truncate(value string, limit int) string {
	// A limit that is not positive keeps nothing. Without this the slice
	// below would be runes[:limit], which panics for a negative limit — in a
	// helper whose entire job is to survive whatever length a caller hands
	// it. The constants at today's call sites are all positive, so this is a
	// guard rather than a bug fix, and it is here because the next caller is
	// the one that decides whether that stays true.
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// Clean makes a value storable in PostgreSQL, which refuses a text value that
// holds a NUL byte or bytes that are not UTF-8. It refuses the whole statement
// carrying such a value, so one client-supplied string costs every other row
// written beside it. SQLite stores the bytes and never shows the problem.
//
// Each run of invalid bytes becomes one U+FFFD and NUL bytes are removed.
// Everything else, tabs and newlines included, is kept as sent.
func Clean(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	return strings.ReplaceAll(value, "\x00", "")
}

// TrimAndTruncate trims surrounding whitespace before truncating.
//
// Use this for a value a person typed into a form — a title, a description
// — where leading and trailing whitespace is input noise, not content, and
// stripping it is the behaviour callers already relied on before this
// package existed.
func TrimAndTruncate(value string, limit int) string {
	return Truncate(strings.TrimSpace(value), limit)
}
