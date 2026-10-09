package console

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// ANSI SGR codes this package ever emits — section 1.5 of the contract is
// explicit that this is the whole palette: dim, bold, the three status
// colours, underline for a table header, and nothing else. The emulator's
// own theme (which follows the app's --ai-* tokens on the web terminal)
// decides the actual hue; these are just the standard SGR defaults.
const (
	ansiReset     = "\x1b[0m"
	ansiBold      = "\x1b[1m"
	ansiDim       = "\x1b[2m"
	ansiUnderline = "\x1b[4m"
	ansiRed       = "\x1b[31m"
	ansiGreen     = "\x1b[32m"
	ansiYellow    = "\x1b[33m"
)

// sanitize strips the control characters out of a value before it reaches a
// terminal.
//
// Everything this package renders that came out of the database is somebody's
// input: a nickname, a conversation title, the text of a message. None of
// those are character-restricted anywhere — `checkNickname` caps the length
// and trims, and that is all — so without this, a regular account could put
// `[2J[H` in its own nickname and wipe the screen of any
// administrator who ran `user show` on it. Over SSH that reaches a real
// emulator, with everything an escape sequence can do there; the web
// terminal's tokeniser honours the clear sequences too, deliberately, because
// `watch` needs them.
//
// The answer is not to decide which escapes are safe. It is that data is
// never control: the console emits its own SGR codes (above) and nothing that
// arrives from a row ever does. ESC is dropped rather than replaced, which
// leaves the rest of the sequence visible as the literal `[2J` — an operator
// seeing junk in a nickname is the correct outcome, and a silent deletion
// would hide that somebody tried.
func sanitize(value string) string {
	value = strings.ToValidUTF8(value, "�")
	if strings.IndexFunc(value, isControl) < 0 {
		return value
	}
	return strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, value)
}

// sanitizeText is sanitize for a value that is prose rather than a cell: a
// feedback body or a reply, where the newlines are the layout. Line feed and
// tab survive; carriage return does not, because "\r" moves the cursor back
// over what was already printed and lets the text after it overwrite a line
// the reader has already seen.
//
// Anyone may file feedback, and an administrator reads it on a terminal, so
// this is the one place a stranger's free text reaches that screen.
func sanitizeText(value string) string {
	// A lone 0x80-0x9f byte is a C1 control to a terminal still in Latin-1,
	// and Go would pass it through a string untouched.
	value = strings.ToValidUTF8(value, "�")
	if strings.IndexFunc(value, isTextControl) < 0 {
		return value
	}
	return strings.Map(func(r rune) rune {
		if isTextControl(r) {
			return -1
		}
		return r
	}, value)
}

func isTextControl(r rune) bool { return r != '\n' && r != '\t' && isControl(r) }

// C0, DEL, and C1 — the last because a lone 0x9B is a CSI introducer in its
// own right on terminals that still decode them.
func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// minColumnWidth is the floor RenderTable shrinks a column to before it
// gives up and lets a row overflow the requested width. Below this, an
// ellipsis alone would out-cost the content it replaces.
const minColumnWidth = 3

// RenderTable writes headers and rows as an aligned, two-space-gapped grid,
// clipped to width (columns below 20 are treated as "unknown", per
// Session.Width's own contract, and default to 100). A cell longer than its
// column is truncated with an ellipsis, by rune, so a Chinese nickname
// clips as cleanly as an ASCII one.
func RenderTable(w io.Writer, width int, colour bool, headers []string, rows [][]string) {
	n := len(headers)
	if n == 0 {
		return
	}
	if width < 20 {
		width = 100
	}

	// Before the widths are measured, not at the write: a column sized from
	// characters that are then dropped is a column of the wrong width.
	headers = sanitizeRow(headers)
	clean := make([][]string, len(rows))
	for i, row := range rows {
		clean[i] = sanitizeRow(row)
	}
	rows = clean

	widths := make([]int, n)
	for i, h := range headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for i := 0; i < n && i < len(row); i++ {
			if l := utf8.RuneCountInString(row[i]); l > widths[i] {
				widths[i] = l
			}
		}
	}

	const gap = 2
	avail := width - gap*(n-1)
	total := 0
	for _, wd := range widths {
		total += wd
	}
	for total > avail {
		widest := 0
		for i := 1; i < n; i++ {
			if widths[i] > widths[widest] {
				widest = i
			}
		}
		if widths[widest] <= minColumnWidth {
			break // every column is at the floor; let the row overflow rather than mangle it further
		}
		widths[widest]--
		total--
	}

	writeRow := func(cells []string, header bool) {
		for i := 0; i < n; i++ {
			if i > 0 {
				io.WriteString(w, "  ")
			}
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			cell = clipToWidth(cell, widths[i])
			pad := widths[i] - utf8.RuneCountInString(cell)
			if header && colour {
				io.WriteString(w, ansiDim)
				io.WriteString(w, ansiUnderline)
				io.WriteString(w, cell)
				io.WriteString(w, ansiReset)
			} else {
				io.WriteString(w, cell)
			}
			if pad > 0 {
				io.WriteString(w, strings.Repeat(" ", pad))
			}
		}
		io.WriteString(w, "\n")
	}

	writeRow(headers, true)
	for _, row := range rows {
		writeRow(row, false)
	}
}

func sanitizeRow(cells []string) []string {
	out := make([]string, len(cells))
	for i, cell := range cells {
		out[i] = sanitize(cell)
	}
	return out
}

func clipToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	if width == 1 {
		return string([]rune(s)[:1])
	}
	r := []rune(s)
	return string(r[:width-1]) + "…"
}

// RenderFields writes a key: value block — the "show" counterpart to
// RenderTable, keys aligned and dimmed when colour is available.
func RenderFields(w io.Writer, colour bool, pairs [][2]string) {
	width := 0
	for _, p := range pairs {
		if l := utf8.RuneCountInString(sanitize(p[0])); l > width {
			width = l
		}
	}
	for _, p := range pairs {
		p[0], p[1] = sanitize(p[0]), sanitize(p[1])
		pad := width - utf8.RuneCountInString(p[0])
		if colour {
			fmt.Fprintf(w, "%s%s%s:%s", ansiDim, p[0], strings.Repeat(" ", pad), ansiReset)
		} else {
			fmt.Fprintf(w, "%s:%s", p[0], strings.Repeat(" ", pad))
		}
		fmt.Fprintf(w, " %s\n", p[1])
	}
}

// RenderJSON pretty-prints an already-encoded JSON response body verbatim —
// the contract's requirement that --json show exactly what the server
// answered, nothing rebuilt from it. A body that somehow is not valid JSON
// (should never happen for an admin response) is written through unchanged
// rather than dropped.
func RenderJSON(w io.Writer, raw []byte) error {
	if len(raw) == 0 {
		_, err := io.WriteString(w, "null\n")
		return err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		_, werr := io.WriteString(w, sanitizeText(string(raw)))
		return werr
	}
	buf.WriteByte('\n')
	_, err := w.Write(escapeJSONControls(buf.Bytes()))
	return err
}

// escapeJSONControls rewrites the characters encoding/json leaves raw — DEL
// and the C1 range, which a terminal may act on — as \u escapes, so the text
// still decodes to the same value and no longer drives a screen. The C0
// controls need no help: JSON cannot carry them unescaped.
func escapeJSONControls(doc []byte) []byte {
	plain := true
	for _, b := range doc {
		if b == 0x7f || b >= 0x80 {
			plain = false
			break
		}
	}
	if plain {
		return doc
	}
	out := make([]byte, 0, len(doc)+16)
	for i := 0; i < len(doc); {
		r, size := utf8.DecodeRune(doc[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			out = append(out, `�`...)
		case r == 0x7f || (r >= 0x80 && r <= 0x9f):
			out = append(out, fmt.Sprintf(`\u%04x`, r)...)
		default:
			out = append(out, doc[i:i+size]...)
		}
		i += size
	}
	return out
}

// renderRowsAsJSON and renderPairsAsJSON are RenderJSON's fallback for a
// command whose --json output has to be synthesised because it never
// called the API — every session command that renders a table or a field
// block, none of which have a server payload to fall back on.
func renderRowsAsJSON(w io.Writer, headers []string, rows [][]string) error {
	out := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		obj := make(map[string]string, len(headers))
		for i, h := range headers {
			if i < len(row) {
				obj[h] = row[i]
			}
		}
		out = append(out, obj)
	}
	enc, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(escapeJSONControls(enc)))
	return err
}

func renderPairsAsJSON(w io.Writer, pairs [][2]string) error {
	obj := make(map[string]string, len(pairs))
	for _, p := range pairs {
		obj[p[0]] = p[1]
	}
	enc, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(escapeJSONControls(enc)))
	return err
}

// Mask truncates a secret to a short, unambiguous hint — "sk-…abcd" — never
// enough to reconstruct the value, always enough to tell two keys apart in
// a list. A secret already masked server-side (the settings redaction's
// "••••••••") is left alone by callers; Mask is only for a console command
// that has the real value in hand, such as a freshly generated API key.
func Mask(secret string) string {
	r := []rune(secret)
	if len(r) <= 8 {
		return strings.Repeat("•", len(r))
	}
	return string(r[:3]) + "…" + string(r[len(r)-4:])
}

// RenderError renders whatever error a command (or the parser) produced,
// and returns the machine-readable code for Result.Code — "" unless err is
// a *CallError, in which case it is the admin API's own code, shown dim
// beside the message. In JSON mode the same information becomes
// {"error":{"message":…,"code":…}} instead of "error: …", so a failing
// command does not break a pipeline expecting JSON on stdout.
func RenderError(w io.Writer, colour, jsonMode bool, err error) string {
	if err == nil {
		return ""
	}
	var callErr *CallError
	code := ""
	if errors.As(err, &callErr) {
		code = callErr.Code
	}
	// A refusal can quote what the caller typed or what a row holds, and it
	// reaches the screen like any other data.
	message := sanitizeText(err.Error())

	if jsonMode {
		body := map[string]any{"message": message}
		if code != "" {
			body["code"] = code
		}
		if enc, encErr := json.MarshalIndent(map[string]any{"error": body}, "", "  "); encErr == nil {
			fmt.Fprintln(w, string(enc))
		}
		return code
	}

	if colour {
		fmt.Fprintf(w, "%serror:%s %s", ansiRed, ansiReset, message)
	} else {
		fmt.Fprintf(w, "error: %s", message)
	}
	if code != "" {
		if colour {
			fmt.Fprintf(w, " %s(%s)%s", ansiDim, code, ansiReset)
		} else {
			fmt.Fprintf(w, " (%s)", code)
		}
	}
	fmt.Fprintln(w)
	return code
}
