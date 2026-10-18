package web

import (
	"bytes"
	"html"
)

// replaceBetween replaces the bytes after the first open marker, up to the next
// closing marker, with value. A byte-level substitution rather than html/template:
// the document is this project's own build output, not anything untrusted, and
// pulling in a templating package to fill in one tag would be the kind of
// dependency AGENTS.md asks to stop and question rather than reach for.
//
// Absent or malformed markup is left untouched: a title an operator cannot see is
// a smaller failure than a handler that panics because a future build changed how
// the shell writes its own tag.
func replaceBetween(index []byte, open, closing, value string) []byte {
	start := bytes.Index(index, []byte(open))
	if start < 0 {
		return index
	}
	valueStart := start + len(open)
	end := bytes.Index(index[valueStart:], []byte(closing))
	if end < 0 {
		return index
	}
	return spliced(index, valueStart, valueStart+end, []byte(value))
}

// replaceTitle swaps the shell's <title> text for the configured browser title.
func replaceTitle(index []byte, title string) []byte {
	// Escaped because, unlike the colour below, this is free text an operator
	// typed into a settings field — it ends up inside markup, not inside an
	// attribute value with its own quoting.
	return replaceBetween(index, "<title>", "</title>", html.EscapeString(title))
}

// replaceThemeColor swaps the shell's static theme-color value for the
// operator's configured one. The tag always exists — a fresh instance ships
// the same neutral default the interface's own theme picker starts from — so
// this only ever finds a value to replace.
func replaceThemeColor(index []byte, color string) []byte {
	// Not escaped: callers only ever pass a value settings.ValidHexColor has
	// already accepted, which is `#` and six hex digits — none of them
	// meaningful to an HTML attribute parser.
	return replaceBetween(index, `<meta name="theme-color" content="`, `"`, color)
}

// replaceFavicon swaps the shell's <link rel="icon"> href value for the
// configured custom logo or favicon URL.
func replaceFavicon(index []byte, faviconURL string) []byte {
	return replaceBetween(index, `<link rel="icon" href="`, `"`, html.EscapeString(faviconURL))
}

// spliced returns index with the bytes between start and end replaced by
// replacement. replaceBetween is its only caller, so the byte arithmetic that
// every substitution depends on is written once.
func spliced(index []byte, start, end int, replacement []byte) []byte {
	out := make([]byte, 0, len(index)-(end-start)+len(replacement))
	out = append(out, index[:start]...)
	out = append(out, replacement...)
	out = append(out, index[end:]...)
	return out
}
