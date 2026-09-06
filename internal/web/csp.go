package web

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"strings"
)

// InlineScriptHashes returns `'sha256-…'` source expressions for every inline
// <script> in the served index.html.
//
// The shell carries exactly one: the few lines that apply a stored theme
// before the first paint, which cannot be deferred to a module without a
// white flash on every dark-mode reload. Hashing it is what lets the Content
// Security Policy stay at `script-src 'self'` instead of opening up
// 'unsafe-inline' for the whole application.
//
// The hash is computed from the file that is actually served, so it stays
// correct across edits and across whatever the bundler did to the markup —
// and from the script the *parser* produces rather than from the file's raw
// bytes. Those two differ whenever the checkout has CRLF in it, and the
// failure is silent in every test that does not drive a browser: the policy
// advertises a hash the browser never asks for, so the one inline script here
// is blocked and the theme it applies before first paint never runs. The white
// flash it exists to prevent comes back, and nothing on the server says so.
func InlineScriptHashes() []string {
	index, err := fs.ReadFile(distFS, "dist/index.html")
	if err != nil {
		return nil
	}
	return inlineScriptHashes(string(index))
}

// inlineScriptHashes is everything above except finding the file, so a test can
// hand it a shell instead of needing a built frontend on disk — and so the
// test exercises this hashing rather than a copy of it.
func inlineScriptHashes(html string) []string {
	var out []string
	for _, body := range inlineScripts(html) {
		sum := sha256.Sum256([]byte(normalizeNewlines(body)))
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return out
}

// normalizeNewlines does to a script body what the HTML parser does to the
// document before any script inside it is seen: CRLF and a lone CR both
// become LF.
func normalizeNewlines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// inlineScripts returns the body of every <script> element that has no src
// attribute. A deliberately small scanner rather than an HTML parser: the
// input is one file in this repository, not untrusted markup.
func inlineScripts(html string) []string {
	var out []string
	rest := html
	for {
		open := strings.Index(rest, "<script")
		if open < 0 {
			return out
		}
		rest = rest[open:]
		tagEnd := strings.IndexByte(rest, '>')
		if tagEnd < 0 {
			return out
		}
		tag := rest[:tagEnd]
		rest = rest[tagEnd+1:]

		close := strings.Index(rest, "</script>")
		if close < 0 {
			return out
		}
		body := rest[:close]
		rest = rest[close+len("</script>"):]

		if !strings.Contains(strings.ToLower(tag), " src") {
			out = append(out, body)
		}
	}
}
