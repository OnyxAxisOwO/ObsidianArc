package console

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// ANSI SGR codes this package ever emits. The palette stays small on purpose:
// dim, bold, three status colours and underline for a table header. Only red,
// for an error, is used today; the other two status colours are added here
// when something prints one, so this list stays what the package actually
// emits. The emulator's own theme (which follows the app's --ai-* tokens on the
// web terminal) decides the actual hue; these are just the standard SGR
// defaults.
const (
	ansiReset     = "\x1b[0m"
	ansiBold      = "\x1b[1m"
	ansiDim       = "\x1b[2m"
	ansiUnderline = "\x1b[4m"
	ansiRed       = "\x1b[31m"
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
	if strings.IndexFunc(value, isStripped) < 0 {
		return value
	}
	return strings.Map(func(r rune) rune {
		if isStripped(r) {
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

func isTextControl(r rune) bool { return r != '\n' && r != '\t' && isStripped(r) }

// C0, DEL, and C1 — the last because a lone 0x9B is a CSI introducer in its
// own right on terminals that still decode them.
func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// isStripped is what sanitize and sanitizeText remove: the control characters,
// and the format characters that change how the text around them is drawn.
func isStripped(r rune) bool { return isControl(r) || isFormatCharacter(r) }

// isFormatCharacter is the bidi overrides and isolates, the direction marks, the
// zero-width characters and the byte-order mark. None of them is drawn itself,
// so they are not control characters, but an override in a stranger's text
// would show an administrator a line that reads differently from what is stored.
// The word joiners, the Mongolian vowel separator, the interlinear annotation
// marks and the language tag block (U+E0001 and U+E0020-E007F, which spell ASCII
// invisibly) are the same kind of character, and hide or reorder text the same
// way. The cost is in the table and field output: a flag sequence such as the
// England subdivision, whose tags are what make it a flag, loses them and shows
// its base emoji instead. --json keeps them, as \u escapes that decode to the
// same characters.
func isFormatCharacter(r rune) bool {
	return r == 0x061c || r == 0x180e || (r >= 0x200b && r <= 0x200f) || (r >= 0x202a && r <= 0x202e) ||
		(r >= 0x2060 && r <= 0x2069) || r == 0xfeff || (r >= 0xfff9 && r <= 0xfffb) ||
		r == 0xe0001 || (r >= 0xe0020 && r <= 0xe007f)
}

// cellWidth is how many terminal cells s occupies. A rune count is not that: a
// Chinese nickname is one rune and two cells, so a row widened by one shifted
// every column after it. Everything that lays text out measures with this.
func cellWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeCells(r)
	}
	return n
}

// RuneWidth is runeCells for the line editor in consolessh. The editor moves its
// cursor by it, so the cursor and the output it edits agree on how far each
// character reaches; a second width table there is how they came to disagree.
func RuneWidth(r rune) int { return runeCells(r) }

// runeCells is the width of one rune in cells: 2 for an East Asian Wide or
// Fullwidth character, 0 for a combining or format character, 1 otherwise. The
// ranges are the ones an administrator's text is made of, not all of Unicode,
// and they are written out here because the project takes no dependency for a
// width table.
func runeCells(r rune) int {
	if isFormatCharacter(r) || unicode.Is(unicode.Mn, r) {
		return 0
	}
	for _, span := range wideSpans {
		if r < span[0] {
			break
		}
		if r <= span[1] {
			return 2
		}
	}
	return 1
}

// wideSpans are the wide ranges as sorted [first, last] pairs, so runeCells can
// stop at the first span that starts past r.
var wideSpans = [][2]rune{
	{0x1100, 0x115f},   // Hangul Jamo
	{0x2329, 0x232a},   // angle brackets, East Asian Wide since Unicode 9
	{0x2e80, 0x303e},   // CJK radicals, symbols and punctuation
	{0x3041, 0x33ff},   // kana and CJK compatibility
	{0x3400, 0x4dbf},   // CJK extension A
	{0x4e00, 0x9fff},   // CJK unified ideographs
	{0xa000, 0xa4cf},   // Yi
	{0xac00, 0xd7a3},   // Hangul syllables
	{0xf900, 0xfaff},   // CJK compatibility ideographs
	{0xfe30, 0xfe4f},   // CJK compatibility forms
	{0xff00, 0xff60},   // fullwidth forms
	{0xffe0, 0xffe6},   // fullwidth signs
	{0x1f300, 0x1f64f}, // emoji and pictographs
	{0x1f900, 0x1f9ff}, // supplemental symbols
	{0x20000, 0x3fffd}, // CJK extensions B and later
}

// minColumnWidth is the floor RenderTable shrinks a column to before it
// gives up and lets a row overflow the requested width. Below this, an
// ellipsis alone would out-cost the content it replaces.
const minColumnWidth = 3

// RenderTable writes headers and rows as an aligned, two-space-gapped grid,
// clipped to width (columns below 20 are treated as "unknown", per
// Session.Width's own contract, and default to 100). A cell longer than its
// column is truncated with an ellipsis, by terminal cell, so a Chinese nickname
// clips and lines up as an ASCII one does.
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
		widths[i] = cellWidth(h)
	}
	for _, row := range rows {
		for i := 0; i < n && i < len(row); i++ {
			if l := cellWidth(row[i]); l > widths[i] {
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
			pad := widths[i] - cellWidth(cell)
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
	if cellWidth(s) <= width {
		return s
	}
	if width == 1 {
		return takeCells(s, 1)
	}
	return takeCells(s, width-1) + "…"
}

// takeCells is the longest prefix of s that fits in cells. A wide character that
// would straddle the edge is dropped rather than split, because half of one
// cannot be drawn.
func takeCells(s string, cells int) string {
	used := 0
	for i, r := range s {
		used += runeCells(r)
		if used > cells {
			return s[:i]
		}
	}
	return s
}

// RenderFields writes a key: value block — the "show" counterpart to
// RenderTable, keys aligned and dimmed when colour is available.
func RenderFields(w io.Writer, colour bool, pairs [][2]string) {
	width := 0
	for _, p := range pairs {
		if l := cellWidth(sanitize(p[0])); l > width {
			width = l
		}
	}
	for _, p := range pairs {
		p[0], p[1] = sanitize(p[0]), sanitize(p[1])
		pad := width - cellWidth(p[0])
		if colour {
			fmt.Fprintf(w, "%s%s%s:%s", ansiDim, p[0], strings.Repeat(" ", pad), ansiReset)
		} else {
			fmt.Fprintf(w, "%s:%s", p[0], strings.Repeat(" ", pad))
		}
		fmt.Fprintf(w, " %s\n", p[1])
	}
}

// RenderJSON pretty-prints an already-encoded JSON response body verbatim —
// the --json requirement that the output be exactly what the server answered,
// nothing rebuilt from it. A body that somehow is not valid JSON
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

// escapeJSONControls rewrites the characters encoding/json leaves raw — DEL,
// the C1 range, and the format characters a stranger's text can reorder or hide
// text with — as \u escapes, so the text still decodes to the same value and no
// longer reaches a screen as itself. The C0 controls need no help: JSON cannot
// carry them unescaped.
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
		case r == 0x7f || (r >= 0x80 && r <= 0x9f) || isFormatCharacter(r):
			out = append(out, jsonEscapeRune(r)...)
		default:
			out = append(out, doc[i:i+size]...)
		}
		i += size
	}
	return out
}

// jsonEscapeRune spells r as \u escapes, as a surrogate pair above U+FFFF: the
// tag characters are past the Basic Multilingual Plane, and a four-digit escape
// of one would not be valid JSON.
func jsonEscapeRune(r rune) string {
	if r <= 0xffff {
		return fmt.Sprintf(`\u%04x`, r)
	}
	hi, lo := utf16.EncodeRune(r)
	return fmt.Sprintf(`\u%04x\u%04x`, hi, lo)
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
		// The code is the server's word for a plugin package's error, and a
		// package's error text is not vetted, so the code is data too: the text
		// output prints it raw, and it is also what the audit records.
		code = sanitize(callErr.Code)
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
