package consolessh

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// lineEditor is a small, dependency-free readline. golang.org/x/term offers
// a raw-mode line reader, but the contract keeps this package to exactly
// golang.org/x/crypto/ssh, so the editing loop, the escape-sequence parser
// and the cursor arithmetic below are all hand-written.
//
// It edits by rune, not by byte: a backspace over a Chinese character must
// remove the whole character, and the redraw math has to know that
// character occupies two terminal columns or every edit after the first
// wide rune drifts the cursor one column right of where it should be. See
// runeWidth.
type lineEditor struct {
	br       *bufio.Reader
	w        io.Writer
	prompt   string
	complete completeFunc
	hist     *history

	buf []rune
	pos int // cursor, in runes

	histIdx int    // -1 = viewing the live draft, not navigating history
	draft   []rune // the draft being edited, saved while histIdx >= 0

	// escape holds what has been seen of an escape sequence so far. Feed
	// accumulates into it across calls rather than reading the rest of the
	// sequence itself — see feedEscape.
	escape []rune
	// escapeFromSearch is set when the Esc that starts escape cancelled a
	// reverse search. Whatever sequence follows is swallowed, and a rune that
	// cannot open one is a key of its own.
	escapeFromSearch bool
	// overflowed is set once a rune has been refused for the length bound, so
	// one paste that runs past it prints a single notice rather than one per rune.
	overflowed bool
	// dropped is set when a rune was refused for the length bound on this line.
	// Enter does not run such a line: its kept prefix can parse as a complete
	// command with the tail that mattered missing, so the line is refused whole.
	dropped bool
	// draftDropped carries dropped across a trip into history and back, since
	// the draft is what Down restores and the flag has to come back with it.
	draftDropped bool
	// lang chooses the language of the notices the editor writes. The session
	// sets it before the first key is read; empty is English.
	lang string

	searching    bool
	searchTerm   []rune
	searchIdx    int    // index into hist.lines of the current match
	searchMatch  string // the line shown as the match; falls back to the pre-search draft
	preSearchBuf []rune
	preSearchPos int
}

// completeFunc mirrors console.Console.Complete's shape without importing
// the console package, so the editor and its tests do not depend on
// whichever engine is driving it — the ssh server is the only thing that
// has to know about *console.Console.
type completeFunc func(line string, bytePos int) (from int, items []editorCompletion)

type editorCompletion struct {
	Value string
	Label string
}

func newLineEditor(r io.Reader, w io.Writer, prompt string, complete completeFunc, hist *history) *lineEditor {
	if hist == nil {
		hist = newHistory()
	}
	return &lineEditor{
		br:       bufio.NewReader(r),
		w:        w,
		prompt:   prompt,
		complete: complete,
		hist:     hist,
		histIdx:  -1,
	}
}

// Start resets the editor for a new line and paints the prompt. Split out
// from ReadLine so the ssh server can drive the reader itself (see Feed):
// while a command is running, the session still has to watch the same
// stream for Ctrl-C, and a second, independent reader on the channel would
// race the bufio.Reader here for bytes already sitting in its buffer.
// Multiplexing both through one NextRune/Feed loop avoids that race
// entirely rather than trying to synchronise two readers.
func (ed *lineEditor) Start() {
	ed.buf = nil
	ed.pos = 0
	ed.histIdx = -1
	ed.draft = nil
	ed.dropped = false
	ed.draftDropped = false
	ed.searching = false
	ed.searchTerm = nil
	ed.searchIdx = -1
	ed.escape = nil
	ed.escapeFromSearch = false
	ed.redraw()
}

// NextRune reads one rune from the underlying stream, decoding UTF-8 as it
// goes (see the package doc on lineEditor for why that matters).
func (ed *lineEditor) NextRune() (rune, error) {
	r, _, err := ed.br.ReadRune()
	return r, err
}

// Feed processes one rune already obtained from NextRune. done is true once
// Enter (including accepting a reverse-search match) produces a line; eof
// is true only for Ctrl-D on an empty line, the session's cue to close.
func (ed *lineEditor) Feed(r rune) (line string, done bool, eof bool) {
	// A lone Esc that cancelled a search is followed by an ordinary key. Only
	// '[' or 'O' can open a sequence, so anything else is fed through again as
	// if the Esc had never come.
	if ed.escapeFromSearch && len(ed.escape) == 1 && r != '[' && r != 'O' {
		ed.escape = nil
		ed.escapeFromSearch = false
		return ed.Feed(r)
	}
	if len(ed.escape) > 0 {
		if ed.feedEscape(r) {
			return "", false, false
		}
		// The Esc did not open a sequence, so only the Esc is dropped and r is an
		// ordinary key. Otherwise Esc then Enter (Alt-Enter) lost the Enter.
		return ed.Feed(r)
	}
	if ed.searching {
		if r == 0x1B {
			// Cancelled at once, not when the rest of an arrow key arrives: a
			// lone Esc has to act now. The bytes of an arrow key that follow are
			// swallowed by feedEscape rather than typed as "[A".
			ed.endSearch(true)
			ed.escape = []rune{r}
			ed.escapeFromSearch = true
			return "", false, false
		}
		if searchDone, matched, submit := ed.handleSearchKey(r); searchDone {
			if !submit {
				return "", false, false
			}
			return ed.finishLine(matched), true, false
		}
		return "", false, false
	}

	switch {
	case r == '\r' || r == '\n':
		if ed.dropped {
			ed.refuseLine()
			return "", false, false
		}
		return ed.finishLine(string(ed.buf)), true, false
	case r == 0x03: // Ctrl-C: abandon the line, idle or not.
		_, _ = ed.w.Write([]byte("^C\r\n"))
		ed.buf = nil
		ed.pos = 0
		ed.histIdx = -1
		ed.dropped = false
		ed.draftDropped = false
		ed.redraw()
	case r == 0x04: // Ctrl-D
		if len(ed.buf) == 0 {
			_, _ = ed.w.Write([]byte("\r\n"))
			return "", false, true
		}
		ed.deleteForward()
	case r == 0x7F || r == 0x08: // Backspace / Ctrl-H
		ed.backspace()
	case r == 0x01: // Ctrl-A
		ed.moveHome()
	case r == 0x05: // Ctrl-E
		ed.moveEnd()
	case r == 0x02: // Ctrl-B
		ed.moveLeft()
	case r == 0x06: // Ctrl-F
		ed.moveRight()
	case r == 0x10: // Ctrl-P
		ed.historyUp()
	case r == 0x0E: // Ctrl-N
		ed.historyDown()
	case r == 0x12: // Ctrl-R
		ed.startSearch()
	case r == 0x17: // Ctrl-W
		ed.deleteWordLeft()
	case r == 0x15: // Ctrl-U
		ed.killToStart()
	case r == 0x0B: // Ctrl-K
		ed.killToEnd()
	case r == 0x0C: // Ctrl-L
		ed.clearScreen()
	case r == 0x09: // Tab
		ed.tab()
	case r == 0x1B: // Escape: the start of a CSI/SS3 sequence, or nothing.
		ed.escape = []rune{r}
		ed.escapeFromSearch = false
	case r < 0x20:
		// Unhandled control byte; nothing in the required table maps to
		// it, and echoing it verbatim would corrupt the terminal.
	default:
		ed.insert(r)
	}
	return "", false, false
}

// finishLine echoes the newline and records history; the return value is
// handed straight back to the caller as the submitted line.
func (ed *lineEditor) finishLine(line string) string {
	_, _ = ed.w.Write([]byte("\r\n"))
	ed.hist.add(line)
	return line
}

// refuseLine discards a line that lost runes to the bound, and clears it so the
// prompt comes back empty. It is not added to history: the history is what Up
// shows, and a line that was never run should not be offered again.
func (ed *lineEditor) refuseLine() {
	_, _ = ed.w.Write([]byte(fmt.Sprintf(noticeLineNotRun.in(ed.lang), maxLineRunes)))
	ed.buf = nil
	ed.pos = 0
	ed.histIdx = -1
	ed.draft = nil
	ed.dropped = false
	ed.draftDropped = false
	ed.overflowed = false
	ed.redraw()
}

// maxLineRunes bounds one command line. The console's longest real command
// is a pasted JSON document for `setting import` or `model import`, which is
// nowhere near this; the bound exists because an authenticated peer can
// otherwise paste without end and the line lives in memory, gets redrawn on
// every keystroke, and is copied into history.
const maxLineRunes = 8192

// maxSearchRunes bounds the reverse-search term. The term and the match beside
// it are written in full on every keystroke, and a match can be a whole history
// line, so the output of a search grows with the square of this figure. Eight
// thousand runes of term would be tens of megabytes from one paste; no search
// needs more than a few hundred.
const maxSearchRunes = 256

// insert puts r at the cursor. Only what changed on the screen is written: at
// the end of the line that is the rune alone, because nothing after the cursor
// moves. Mid-line, the rune and the text after it are written, and the cursor
// is walked back over that text. Redrawing the whole line here made a paste of
// n runes write about n squared, since every rune repainted everything before it.
func (ed *lineEditor) insert(r rune) {
	if len(ed.buf) >= maxLineRunes {
		// A paste past the bound is refused here, and the line is refused whole
		// on Enter (see dropped). Saying so once is what tells the person why
		// nothing ran; the flag clears when the line has room again, so the next
		// overflow is reported too.
		ed.dropped = true
		if !ed.overflowed {
			ed.overflowed = true
			_, _ = ed.w.Write([]byte(fmt.Sprintf(noticeLineTooLong.in(ed.lang), maxLineRunes)))
			ed.redraw()
		}
		return
	}
	ed.overflowed = false
	ed.buf = append(ed.buf, 0)
	copy(ed.buf[ed.pos+1:], ed.buf[ed.pos:])
	ed.buf[ed.pos] = r
	ed.pos++

	if ed.pos == len(ed.buf) {
		_, _ = ed.w.Write([]byte(string(r)))
		return
	}
	tail := ed.buf[ed.pos:]
	var b strings.Builder
	b.WriteRune(r)
	b.WriteString(string(tail))
	if width := displayWidth(tail); width > 0 {
		b.WriteString("\x1b[")
		b.WriteString(strconv.Itoa(width))
		b.WriteString("D")
	}
	_, _ = ed.w.Write([]byte(b.String()))
}

func (ed *lineEditor) backspace() {
	if ed.pos == 0 {
		return
	}
	ed.buf = append(ed.buf[:ed.pos-1], ed.buf[ed.pos:]...)
	ed.pos--
	ed.redraw()
}

func (ed *lineEditor) deleteForward() {
	if ed.pos >= len(ed.buf) {
		return
	}
	ed.buf = append(ed.buf[:ed.pos], ed.buf[ed.pos+1:]...)
	ed.redraw()
}

func (ed *lineEditor) moveHome() {
	ed.pos = 0
	ed.redraw()
}

func (ed *lineEditor) moveEnd() {
	ed.pos = len(ed.buf)
	ed.redraw()
}

// moveLeft and moveRight move the terminal cursor over the one rune they pass,
// by its width. Repainting the line for that was a reply of about the line's
// length to a three-byte key.
func (ed *lineEditor) moveLeft() {
	if ed.pos > 0 {
		ed.pos--
		if width := runeWidth(ed.buf[ed.pos]); width > 0 {
			_, _ = ed.w.Write([]byte("\x1b[" + strconv.Itoa(width) + "D"))
		}
	}
}

func (ed *lineEditor) moveRight() {
	if ed.pos < len(ed.buf) {
		width := runeWidth(ed.buf[ed.pos])
		ed.pos++
		if width > 0 {
			_, _ = ed.w.Write([]byte("\x1b[" + strconv.Itoa(width) + "C"))
		}
	}
}

func isWordSpace(r rune) bool { return r == ' ' || r == '\t' }

func (ed *lineEditor) deleteWordLeft() {
	if ed.pos == 0 {
		return
	}
	end := ed.pos
	i := ed.pos
	for i > 0 && isWordSpace(ed.buf[i-1]) {
		i--
	}
	for i > 0 && !isWordSpace(ed.buf[i-1]) {
		i--
	}
	ed.buf = append(ed.buf[:i], ed.buf[end:]...)
	ed.pos = i
	ed.clearRefusalIfEmpty()
	ed.redraw()
}

func (ed *lineEditor) killToStart() {
	if ed.pos == 0 {
		return
	}
	ed.buf = append([]rune{}, ed.buf[ed.pos:]...)
	ed.pos = 0
	ed.clearRefusalIfEmpty()
	ed.redraw()
}

func (ed *lineEditor) killToEnd() {
	if ed.pos >= len(ed.buf) {
		return
	}
	ed.buf = append([]rune{}, ed.buf[:ed.pos]...)
	ed.clearRefusalIfEmpty()
	ed.redraw()
}

// clearRefusalIfEmpty lifts the refusal once a kill has taken the whole line.
// What was refused went with it, so the next line typed is not the one that
// lost runes. A kill that leaves text keeps the refusal, because the runes
// that were lost may still be in what is left.
func (ed *lineEditor) clearRefusalIfEmpty() {
	if len(ed.buf) == 0 {
		ed.dropped = false
	}
}

// refuseOnEnter marks the line being edited as one that lost runes before the
// editor saw them, so Enter refuses it as it refuses an overlong paste. The
// session calls it for keys discarded from the type-ahead queue while a
// command ran, which are the same loss.
func (ed *lineEditor) refuseOnEnter() {
	ed.dropped = true
}

func (ed *lineEditor) clearScreen() {
	// \x1b[H homes the cursor, \x1b[2J clears the visible screen. Neither
	// touches the scrollback, so this matches Ctrl-L at a real shell rather
	// than wiping history the reader might still want.
	_, _ = ed.w.Write([]byte("\x1b[H\x1b[2J"))
	ed.redraw()
}

func (ed *lineEditor) historyUp() {
	lines := ed.hist.lines
	if len(lines) == 0 {
		return
	}
	if ed.histIdx == -1 {
		ed.draft = append([]rune{}, ed.buf...)
		ed.draftDropped = ed.dropped
		ed.histIdx = len(lines) - 1
	} else if ed.histIdx > 0 {
		ed.histIdx--
	} else {
		return
	}
	ed.buf = []rune(lines[ed.histIdx])
	ed.dropped = false
	ed.pos = len(ed.buf)
	ed.redraw()
}

func (ed *lineEditor) historyDown() {
	if ed.histIdx == -1 {
		return
	}
	lines := ed.hist.lines
	ed.histIdx++
	if ed.histIdx >= len(lines) {
		ed.histIdx = -1
		ed.buf = ed.draft
		ed.dropped = ed.draftDropped
		ed.draft = nil
		ed.draftDropped = false
	} else {
		ed.buf = []rune(lines[ed.histIdx])
		ed.dropped = false
	}
	ed.pos = len(ed.buf)
	ed.redraw()
}

// maxEscapeRunes bounds an escape sequence while it is being collected. The
// longest one the editor acts on is a few bytes; a peer that never sends a
// final byte must not be able to grow the buffer without limit.
const maxEscapeRunes = 16

// feedEscape accumulates an escape sequence one rune at a time.
//
// It used to read the rest of the sequence straight off ed.br, which is the
// same bufio.Reader the ssh session's own goroutine is concurrently reading
// for the next keystroke — two goroutines mutating one bufio.Reader, which
// is not safe for concurrent use. An arrow key was enough to trip it, and
// the failure was not limited to a garbled line: a slice-bounds panic inside
// bufio in an unrecovered goroutine takes the whole process down, so one
// keystroke in one console session could stop the server.
//
// Start's doc comment already said only one goroutine may read this stream.
// Feeding the sequence through the same NextRune/Feed path as every other
// rune is what makes that true, rather than merely stated.
//
// A CSI or SS3 sequence is its introducer, then parameter and intermediate
// bytes (0x20-0x3F), then one final byte (0x40-0x7E). Collecting up to the
// final byte is what lets a sequence with a parameter, such as PageUp's
// ESC [ 5 ~ or a modified arrow's ESC [ 1 ; 5 C, be consumed whole instead of
// having its tail typed into the line.
//
// It reports whether r was consumed. A second byte that cannot open a sequence
// is not: the Esc before it was a lone key or an Alt prefix, and r must be
// handled as ordinary input by the caller.
func (ed *lineEditor) feedEscape(r rune) bool {
	if r == 0x1B {
		// A new Esc abandons whatever was pending and starts over. It is not the
		// Esc that cancelled a search, so the swallowing that goes with that Esc
		// must not carry over to the sequence it opens.
		ed.escape = []rune{r}
		ed.escapeFromSearch = false
		return true
	}
	ed.escape = append(ed.escape, r)
	if len(ed.escape) == 2 {
		if r != '[' && r != 'O' {
			ed.escape = nil
			return false
		}
		return true
	}
	if !plausibleEscapeTail(ed.escape) {
		// The Esc was a key of its own, and what came after it is typed text. The
		// rune that failed the check is handed back to the caller, which feeds it
		// again; only what was collected before it is typed here.
		typed := ed.escape[1 : len(ed.escape)-1]
		ed.escape = nil
		ed.escapeFromSearch = false
		for _, t := range typed {
			ed.insert(t)
		}
		return false
	}
	if len(ed.escape) > maxEscapeRunes || r < 0x20 || r > 0x7E {
		ed.escape = nil
		return true
	}
	if r < 0x40 {
		return true
	}
	seq := ed.escape
	ed.escape = nil
	ed.runEscape(seq)
	return true
}

// plausibleEscapeTail reports whether the bytes after an Esc can still be a key
// the editor knows. When they cannot, the Esc was a key of its own and the
// letters after it are typed; without this check "Esc Obvious" lost its O and b
// to a sequence the O seemed to open. ESC O takes one final byte, and only the
// SS3 keys use one. ESC [ takes parameters and then a final byte. The finals
// listed are the ones the editor acts on or swallows on purpose, Shift-Tab (Z)
// among them, so ESC [ b is typed and ESC [ Z stays swallowed.
func plausibleEscapeTail(seq []rune) bool {
	r := seq[len(seq)-1]
	switch seq[1] {
	case 'O':
		return strings.ContainsRune("ABCDHFPQRS", r)
	case '[':
		if r == ';' || (r >= '0' && r <= '9') {
			return true
		}
		return strings.ContainsRune("ABCDHFZ~", r)
	}
	return true
}

// runEscape acts on one complete escape sequence: the Esc, its introducer,
// any parameters, and the final byte. Only the unparameterised arrows, Home
// and End, and the tilde forms for Home, Delete and End are acted on. Every
// other sequence, including the modified forms, is dropped whole.
func (ed *lineEditor) runEscape(seq []rune) {
	fromSearch := ed.escapeFromSearch
	ed.escapeFromSearch = false
	if fromSearch {
		// The search has already been cancelled by its Esc. The arrow keys
		// that followed it have nothing to act on.
		return
	}

	final := seq[len(seq)-1]
	params := string(seq[2 : len(seq)-1])
	if params == "" {
		switch final {
		case 'A':
			ed.historyUp()
		case 'B':
			ed.historyDown()
		case 'C':
			ed.moveRight()
		case 'D':
			ed.moveLeft()
		case 'H':
			ed.moveHome()
		case 'F':
			ed.moveEnd()
		}
		return
	}
	if final == '~' {
		switch params {
		case "1", "7":
			ed.moveHome()
		case "3":
			ed.deleteForward()
		case "4", "8":
			ed.moveEnd()
		}
	}
}

func (ed *lineEditor) tab() {
	if ed.complete == nil {
		return
	}
	fromByte, items := ed.complete(string(ed.buf), byteOffsetForRuneIndex(ed.buf, ed.pos))
	if len(items) == 0 {
		return
	}
	if len(items) == 1 {
		ed.applyCompletion(fromByte, items[0].Value)
		return
	}
	ed.printCompletionList(items)
}

func (ed *lineEditor) applyCompletion(fromByte int, value string) {
	fromRune := runeIndexForByteOffset(string(ed.buf), fromByte)
	if fromRune < 0 || fromRune > ed.pos || fromRune > len(ed.buf) {
		fromRune = ed.pos
	}
	tail := append([]rune{}, ed.buf[ed.pos:]...)
	head := append([]rune{}, ed.buf[:fromRune]...)
	inserted := []rune(value)
	ed.buf = append(append(head, inserted...), tail...)
	ed.pos = fromRune + len(inserted)
	ed.redraw()
}

// printCompletionList shows several candidates the way a shell does: drop
// below the prompt, print them, then redraw the prompt and the line
// unchanged so the reader keeps typing from where they were.
func (ed *lineEditor) printCompletionList(items []editorCompletion) {
	var b strings.Builder
	b.WriteString("\r\n")
	for i, item := range items {
		if i > 0 {
			b.WriteString("  ")
		}
		label := item.Label
		if label == "" {
			label = item.Value
		}
		b.WriteString(label)
	}
	b.WriteString("\r\n")
	_, _ = ed.w.Write([]byte(b.String()))
	ed.redraw()
}

func (ed *lineEditor) startSearch() {
	ed.searching = true
	ed.searchTerm = nil
	ed.searchIdx = len(ed.hist.lines)
	ed.preSearchBuf = append([]rune{}, ed.buf...)
	ed.preSearchPos = ed.pos
	ed.searchMatch = string(ed.preSearchBuf)
	ed.redrawSearch(ed.searchMatch)
}

// handleSearchKey processes one rune while Ctrl-R search is active. done is
// true once the caller should leave search mode; submit is true only when
// that exit is an Enter, in which case line is what ReadLine should return.
//
// Enter and the other exits read ed.searchMatch rather than recomputing a
// match: searchFrom leaves ed.searchIdx sitting *at* the matched entry, so a
// second scan starting "before" it would look one entry too far back and
// could report no match at all where there plainly was one on screen.
func (ed *lineEditor) handleSearchKey(r rune) (done bool, line string, submit bool) {
	switch {
	case r == '\r' || r == '\n':
		matched := ed.searchMatch
		ed.endSearch(false)
		return true, matched, true
	case r == 0x12: // Ctrl-R again: look further back for the same term.
		ed.searchFrom(ed.searchIdx)
		ed.redrawSearch(ed.searchMatch)
		return false, "", false
	case r == 0x07 || r == 0x03: // Ctrl-G, Ctrl-C: cancel. Esc is handled in Feed.
		ed.endSearch(true)
		return true, "", false
	case r == 0x7F || r == 0x08: // Backspace: shrink the search term.
		if len(ed.searchTerm) > 0 {
			ed.searchTerm = ed.searchTerm[:len(ed.searchTerm)-1]
			ed.searchFrom(len(ed.hist.lines))
			ed.redrawSearch(ed.searchMatch)
		}
		return false, "", false
	case r < 0x20:
		// Any other control key ends the search; the match found so far
		// becomes the live line and the key is otherwise dropped, which
		// keeps this simple readline from needing a full mode-reentry path.
		ed.endSearch(false)
		return true, "", false
	default:
		if len(ed.searchTerm) >= maxSearchRunes {
			return false, "", false
		}
		ed.searchTerm = append(ed.searchTerm, r)
		ed.searchFrom(len(ed.hist.lines))
		ed.redrawSearch(ed.searchMatch)
		return false, "", false
	}
}

// searchFrom scans strictly before index `from` for the nearest history line
// containing the current term, and updates searchIdx/searchMatch when it
// finds one. A run that finds nothing leaves the previous match on screen
// rather than blanking it — pressing Ctrl-R past the oldest match should not
// make the line the reader was just looking at disappear.
func (ed *lineEditor) searchFrom(from int) {
	term := string(ed.searchTerm)
	if term == "" {
		ed.searchIdx = len(ed.hist.lines)
		ed.searchMatch = string(ed.preSearchBuf)
		return
	}
	for i := from - 1; i >= 0; i-- {
		if strings.Contains(ed.hist.lines[i], term) {
			ed.searchIdx = i
			ed.searchMatch = ed.hist.lines[i]
			return
		}
	}
}

func (ed *lineEditor) endSearch(cancelled bool) {
	ed.searching = false
	if cancelled {
		ed.buf = ed.preSearchBuf
		ed.pos = ed.preSearchPos
	} else {
		ed.buf = []rune(ed.searchMatch)
		ed.pos = len(ed.buf)
		// The match is a history line, which is never over the bound. Cancelling
		// keeps the flag, because the buffer it restores may be the overlong one.
		ed.dropped = false
	}
	ed.preSearchBuf = nil
	ed.redraw()
}

func (ed *lineEditor) redrawSearch(match string) {
	var b strings.Builder
	b.WriteByte('\r')
	b.WriteString("(reverse-i-search)`")
	b.WriteString(string(ed.searchTerm))
	b.WriteString("': ")
	b.WriteString(match)
	b.WriteString("\x1b[K")
	_, _ = ed.w.Write([]byte(b.String()))
}

// redraw repaints the current line in place: carriage return, prompt, the
// buffer, an erase-to-end-of-line, then a cursor move back to pos. There is
// no full-screen repaint, so this is one write no matter how long the line
// is.
func (ed *lineEditor) redraw() {
	var b strings.Builder
	b.WriteByte('\r')
	b.WriteString(ed.prompt)
	b.WriteString(string(ed.buf))
	b.WriteString("\x1b[K")
	trailing := displayWidth(ed.buf[ed.pos:])
	if trailing > 0 {
		b.WriteString("\x1b[")
		b.WriteString(strconv.Itoa(trailing))
		b.WriteString("D")
	}
	_, _ = ed.w.Write([]byte(b.String()))
}

// byteOffsetForRuneIndex converts a cursor position expressed in runes (how
// the editor tracks it) to the byte offset console.Console.Complete expects.
func byteOffsetForRuneIndex(buf []rune, idx int) int {
	if idx <= 0 {
		return 0
	}
	if idx >= len(buf) {
		idx = len(buf)
	}
	return len(string(buf[:idx]))
}

// runeIndexForByteOffset is the inverse, for applying a completion's `from`
// (a byte offset from console.Completion) back onto the rune buffer.
func runeIndexForByteOffset(s string, offset int) int {
	if offset <= 0 {
		return 0
	}
	count := 0
	for i := range s {
		if i >= offset {
			return count
		}
		count++
	}
	return count
}

// history is one session's command history: capped, and deduplicated only
// against the immediately preceding entry so repeating a command on purpose
// (running the same health check twice) still works.
type history struct {
	lines []string
}

const maxHistoryLines = 500

func newHistory() *history { return &history{} }

func (h *history) add(line string) {
	if line == "" {
		return
	}
	if n := len(h.lines); n > 0 && h.lines[n-1] == line {
		return
	}
	h.lines = append(h.lines, line)
	if len(h.lines) > maxHistoryLines {
		h.lines = h.lines[len(h.lines)-maxHistoryLines:]
	}
}

// displayWidth sums the terminal column width of runes, honouring East
// Asian Wide and Fullwidth characters. Without it, redraw's cursor-move
// arithmetic assumes one column per rune, and the cursor drifts left of
// where it should be on every line that contains a CJK character — which,
// in an interface that ships in Chinese, is most of them.
func displayWidth(runes []rune) int {
	total := 0
	for _, r := range runes {
		total += runeWidth(r)
	}
	return total
}

// runeWidth is a condensed East Asian Width table: wide/fullwidth ranges
// return 2, everything else (including combining and control characters,
// which this editor never puts in the buffer) returns 1 or 0.
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 0x20 || r == 0x7F:
		return 0
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r == 0x2329, r == 0x232A,
		r >= 0x2E80 && r <= 0x303E, // CJK Radicals .. CJK Symbols and Punctuation
		r >= 0x3041 && r <= 0x33FF, // Hiragana .. CJK Compatibility
		r >= 0x3400 && r <= 0x4DBF, // CJK Unified Ideographs Extension A
		r >= 0x4E00 && r <= 0x9FFF, // CJK Unified Ideographs
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul Syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK Compatibility Ideographs
		r >= 0xFE30 && r <= 0xFE4F, // CJK Compatibility Forms
		r >= 0xFF00 && r <= 0xFF60, // Fullwidth Forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x20000 && r <= 0x2FFFD, // CJK Extension B and beyond
		r >= 0x30000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}
