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
// correct across edits and across whatever the bundler did to the markup.
func InlineScriptHashes() []string {
	index, err := fs.ReadFile(distFS, "dist/index.html")
	if err != nil {
		return nil
	}
	var out []string
	for _, body := range inlineScripts(string(index)) {
		sum := sha256.Sum256([]byte(body))
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return out
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
