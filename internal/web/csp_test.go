package web

import (
	"strings"
	"testing"
)

// A browser hashes the script the HTML parser handed it, and parsing turns
// CRLF and lone CR into LF before any script is seen. Hashing the file's raw
// bytes therefore produces a hash no browser ever asks for on a checkout that
// has CRLF in it — the policy blocks the shell's own inline script, and the
// theme it applies before first paint stops running.
//
// Nothing else catches this. The server is happy, every Go test passes, and
// the only symptom is a white flash on a dark-mode reload plus one console
// line nobody is looking at.
func TestInlineScriptHashDoesNotDependOnLineEndings(t *testing.T) {
	const shell = "<html><head>\n" +
		"<script>\n  const stored = read();\n  apply(stored);\n</script>\n" +
		"<script type=\"module\" src=\"/assets/app.js\"></script>\n" +
		"</head></html>"

	lf := inlineScriptHashes(shell)
	crlf := inlineScriptHashes(strings.ReplaceAll(shell, "\n", "\r\n"))

	if len(lf) != 1 {
		t.Fatalf("hashed %d scripts, want 1: the one carrying src must be skipped", len(lf))
	}
	if len(crlf) != len(lf) || crlf[0] != lf[0] {
		t.Fatalf("a CRLF shell hashed to %v and an LF shell to %v; a browser asks for the second either way", crlf, lf)
	}
}

// normalizeNewlines is only ever fed a script body, but it is the thing the
// policy's correctness rests on, so its edges are worth pinning.
func TestNewlineNormalization(t *testing.T) {
	for input, want := range map[string]string{
		"a\r\nb":   "a\nb",
		"a\rb":     "a\nb",
		"a\nb":     "a\nb",
		"a\r\n\rb": "a\n\nb",
		"plain":    "plain",
	} {
		if got := normalizeNewlines(input); got != want {
			t.Errorf("normalizeNewlines(%q) = %q, want %q", input, got, want)
		}
	}
}
