package consolessh

import (
	"bytes"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
)

// ReadLine drives Start/NextRune/Feed to completion. Only these tests want a
// whole line at once: the ssh server needs the finer grain (see Feed's doc
// comment), so this lives here rather than beside the editor.
func (ed *lineEditor) ReadLine() (string, error) {
	ed.Start()
	for {
		r, err := ed.NextRune()
		if err != nil {
			return "", err
		}
		line, done, eof := ed.Feed(r)
		if eof {
			return "", io.EOF
		}
		if done {
			return line, nil
		}
	}
}

// syncBuffer collects the editor's output from a background reader
// goroutine so writes on the pipe never block waiting for the test to get
// around to reading them.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// newPipeEditor wires a lineEditor to one end of an in-memory net.Pipe and
// hands the test the other end, with a goroutine continuously draining the
// editor's output into a syncBuffer so redraws never block on the test
// getting around to reading them.
func newPipeEditor(t *testing.T, complete completeFunc) (*lineEditor, net.Conn, *syncBuffer) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	out := &syncBuffer{}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := client.Read(buf)
			if n > 0 {
				out.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	editor := newLineEditor(server, server, "> ", complete, newHistory())
	return editor, client, out
}

// drive writes input to the client end and waits for one ReadLine call on
// the editor side to finish, returning what it returned.
func drive(t *testing.T, editor *lineEditor, client net.Conn, input string) (string, error) {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := editor.ReadLine()
		done <- result{line, err}
	}()

	if _, err := client.Write([]byte(input)); err != nil {
		t.Fatalf("write input: %v", err)
	}

	select {
	case r := <-done:
		return r.line, r.err
	case <-time.After(2 * time.Second):
		t.Fatal("ReadLine did not return in time")
		return "", nil
	}
}

func TestLineEditorSubmitsAPlainLine(t *testing.T) {
	editor, client, out := newPipeEditor(t, nil)
	line, err := drive(t, editor, client, "help\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "help" {
		t.Fatalf("got %q, want %q", line, "help")
	}
	if !strings.HasPrefix(out.String(), "\r> ") {
		t.Fatalf("output %q does not start with the initial prompt redraw", out.String())
	}
}

func TestLineEditorBackspaceRemovesOneRune(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	// "helpp" then one Backspace corrects the typo before Enter.
	line, err := drive(t, editor, client, "helpp\x7F\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "help" {
		t.Fatalf("got %q, want %q", line, "help")
	}
}

func TestLineEditorUTF8EditsByRuneNotByte(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	// 你好 is two runes / six UTF-8 bytes. One Backspace must remove the
	// whole trailing rune (好), not one of its three bytes — a byte-wise
	// backspace would leave an invalid UTF-8 tail instead of 你.
	line, err := drive(t, editor, client, "你好\x7F吗\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "你吗" {
		t.Fatalf("got %q, want %q (backspace must delete by rune)", line, "你吗")
	}
}

func TestLineEditorLeftArrowInsertsMidLine(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	// "ac", Left, insert "b" between them.
	line, err := drive(t, editor, client, "ac\x1b[Db\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "abc" {
		t.Fatalf("got %q, want %q", line, "abc")
	}
}

func TestLineEditorSS3ArrowVariant(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	// Some clients send ESC O D for Left in application cursor-key mode.
	line, err := drive(t, editor, client, "ac\x1bODb\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "abc" {
		t.Fatalf("got %q, want %q", line, "abc")
	}
}

func TestLineEditorHomeAndEnd(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	// "bc", Home (ESC[H), insert "a" -> "abc", End (ESC[F), insert "d".
	line, err := drive(t, editor, client, "bc\x1b[Ha\x1b[Fd\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "abcd" {
		t.Fatalf("got %q, want %q", line, "abcd")
	}
}

func TestLineEditorHomeAndEndTildeForm(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	// The ESC [ 1~ / ESC [ 4~ forms some terminals send for Home/End.
	line, err := drive(t, editor, client, "bc\x1b[1~a\x1b[4~d\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "abcd" {
		t.Fatalf("got %q, want %q", line, "abcd")
	}
}

func TestLineEditorDeleteKey(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	// "abc", Home, Delete (ESC[3~) removes the 'a' in front of the cursor.
	line, err := drive(t, editor, client, "abc\x1b[H\x1b[3~\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "bc" {
		t.Fatalf("got %q, want %q", line, "bc")
	}
}

func TestLineEditorCtrlAAndCtrlE(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	// Ctrl-A / Ctrl-E are the control-key equivalents of Home/End.
	line, err := drive(t, editor, client, "bc\x01a\x05d\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "abcd" {
		t.Fatalf("got %q, want %q", line, "abcd")
	}
}

func TestLineEditorCtrlBAndCtrlF(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	// Ctrl-B / Ctrl-F are the control-key equivalents of Left/Right.
	line, err := drive(t, editor, client, "ac\x02b\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "abc" {
		t.Fatalf("got %q, want %q", line, "abc")
	}
}

func TestLineEditorCtrlWDeletesWordLeft(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	// "foo bar", Ctrl-W removes "bar" but keeps the preceding space, then
	// "baz" is typed in its place.
	line, err := drive(t, editor, client, "foo bar\x17baz\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "foo baz" {
		t.Fatalf("got %q, want %q", line, "foo baz")
	}
}

func TestLineEditorCtrlUKillsToStart(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	// "hello world", Left x6 to just before "world", Ctrl-U kills "hello ",
	// leaving "world"; then "hi " is typed in front.
	line, err := drive(t, editor, client, "hello world\x1b[D\x1b[D\x1b[D\x1b[D\x1b[D\x15hi \r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "hi world" {
		t.Fatalf("got %q, want %q", line, "hi world")
	}
}

func TestLineEditorCtrlKKillsToEnd(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	// "hello world", Ctrl-A, Right x5 to just after "hello", Ctrl-K removes
	// " world".
	line, err := drive(t, editor, client, "hello world\x01\x06\x06\x06\x06\x06\x0B\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "hello" {
		t.Fatalf("got %q, want %q", line, "hello")
	}
}

func TestLineEditorCtrlCAbandonsTheLineButNotTheSession(t *testing.T) {
	editor, client, out := newPipeEditor(t, nil)
	// Ctrl-C on a non-empty line clears it and echoes ^C, but ReadLine
	// keeps waiting for a fresh line rather than returning.
	line, err := drive(t, editor, client, "garbage\x03done\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "done" {
		t.Fatalf("got %q, want %q", line, "done")
	}
	if !strings.Contains(out.String(), "^C") {
		t.Fatalf("output %q does not echo ^C", out.String())
	}
}

func TestLineEditorCtrlDOnEmptyLineReturnsEOF(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	_, err := drive(t, editor, client, "\x04")
	if err != io.EOF {
		t.Fatalf("got err %v, want io.EOF", err)
	}
}

func TestLineEditorCtrlDOnNonEmptyLineDeletesForward(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	// "abc", Home, Ctrl-D deletes the 'a' in front of the cursor rather
	// than ending the session, because the line is not empty.
	line, err := drive(t, editor, client, "abc\x01\x04\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "bc" {
		t.Fatalf("got %q, want %q", line, "bc")
	}
}

func TestLineEditorHistoryUpAndDown(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)

	if line, err := drive(t, editor, client, "first cmd\r"); err != nil || line != "first cmd" {
		t.Fatalf("first line: %q, %v", line, err)
	}
	if line, err := drive(t, editor, client, "second cmd\r"); err != nil || line != "second cmd" {
		t.Fatalf("second line: %q, %v", line, err)
	}

	// Up, Up recalls the older entry; Down goes back to the newer one.
	line, err := drive(t, editor, client, "\x1b[A\x1b[A\x1b[B\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "second cmd" {
		t.Fatalf("got %q, want %q", line, "second cmd")
	}
}

func TestLineEditorHistoryDownPastNewestRestoresDraft(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	if _, err := drive(t, editor, client, "only cmd\r"); err != nil {
		t.Fatalf("seed history: %v", err)
	}
	// Start a fresh draft, go Up into history, then Down past the newest
	// entry: the draft that was being typed should come back.
	line, err := drive(t, editor, client, "dra\x1b[A\x1b[Bft\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "draft" {
		t.Fatalf("got %q, want %q", line, "draft")
	}
}

func TestLineEditorCtrlPAndCtrlNAreHistoryAliases(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	if _, err := drive(t, editor, client, "alpha\r"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	line, err := drive(t, editor, client, "\x10\r") // Ctrl-P
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "alpha" {
		t.Fatalf("got %q, want %q", line, "alpha")
	}
}

func TestLineEditorTabCompletesSingleMatch(t *testing.T) {
	complete := func(line string, pos int) (int, []editorCompletion) {
		if line == "user li" {
			return 5, []editorCompletion{{Value: "list", Label: "user list"}}
		}
		return 0, nil
	}
	editor, client, _ := newPipeEditor(t, complete)
	line, err := drive(t, editor, client, "user li\t\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "user list" {
		t.Fatalf("got %q, want %q", line, "user list")
	}
}

func TestLineEditorTabListsSeveralMatches(t *testing.T) {
	complete := func(line string, pos int) (int, []editorCompletion) {
		if line == "user " {
			return 5, []editorCompletion{
				{Value: "list", Label: "user list"},
				{Value: "edit", Label: "user edit"},
			}
		}
		return 0, nil
	}
	editor, client, out := newPipeEditor(t, complete)
	line, err := drive(t, editor, client, "user \tlist\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	// The line is untouched by a multi-match Tab; the reader kept typing.
	if line != "user list" {
		t.Fatalf("got %q, want %q", line, "user list")
	}
	if !strings.Contains(out.String(), "user list") || !strings.Contains(out.String(), "user edit") {
		t.Fatalf("output %q does not list both candidates", out.String())
	}
}

func TestLineEditorReverseSearchFindsAndAcceptsAMatch(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	if _, err := drive(t, editor, client, "user list\r"); err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	if _, err := drive(t, editor, client, "model list\r"); err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	// Ctrl-R, type "user", Enter accepts the match.
	line, err := drive(t, editor, client, "\x12user\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "user list" {
		t.Fatalf("got %q, want %q", line, "user list")
	}
}

func TestLineEditorReverseSearchCtrlCCancelsBackToOriginalLine(t *testing.T) {
	editor, client, _ := newPipeEditor(t, nil)
	if _, err := drive(t, editor, client, "user list\r"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Type a draft, enter search, cancel with Ctrl-C, and the draft should
	// still be there to finish typing.
	line, err := drive(t, editor, client, "dra\x12user\x03ft\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "draft" {
		t.Fatalf("got %q, want %q", line, "draft")
	}
}

func TestLineEditorCtrlLClearsScreenAndRedrawsLine(t *testing.T) {
	editor, client, out := newPipeEditor(t, nil)
	line, err := drive(t, editor, client, "abc\x0c\r")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "abc" {
		t.Fatalf("got %q, want %q", line, "abc")
	}
	if !strings.Contains(out.String(), "\x1b[H\x1b[2J") {
		t.Fatalf("output %q missing the clear-screen sequence", out.String())
	}
}

func TestLineEditorRedrawMovesCursorPastWideRunes(t *testing.T) {
	editor, client, out := newPipeEditor(t, nil)
	// 中 occupies two columns; after typing it and moving left, the redraw
	// must move the cursor back two columns, not one, or a second edit
	// would land in the wrong place. The bug this guards against: using
	// len(runes) instead of displayWidth for the trailing cursor-left move.
	if _, err := drive(t, editor, client, "中\x1b[D\r"); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if !strings.Contains(out.String(), "\x1b[2D") {
		t.Fatalf("output %q does not move the cursor 2 columns for a wide rune", out.String())
	}
}

func TestHistoryDeduplicatesOnlyConsecutiveRepeats(t *testing.T) {
	h := newHistory()
	h.add("a")
	h.add("a")
	h.add("b")
	h.add("a")
	if got := h.lines; !equalStrings(got, []string{"a", "b", "a"}) {
		t.Fatalf("got %v", got)
	}
}

func TestHistoryCapsAt500Entries(t *testing.T) {
	h := newHistory()
	for i := 0; i < 600; i++ {
		h.add(strconv.Itoa(i))
	}
	if len(h.lines) != maxHistoryLines {
		t.Fatalf("got %d entries, want %d", len(h.lines), maxHistoryLines)
	}
	if h.lines[0] != strconv.Itoa(100) {
		t.Fatalf("oldest surviving entry is %q, want %q (the first 100 should have been evicted)", h.lines[0], strconv.Itoa(100))
	}
	if h.lines[len(h.lines)-1] != strconv.Itoa(599) {
		t.Fatalf("newest entry is %q, want %q", h.lines[len(h.lines)-1], strconv.Itoa(599))
	}
}

func TestRuneWidthTreatsCJKAsTwoColumns(t *testing.T) {
	cases := []struct {
		r    rune
		want int
	}{
		{'a', 1},
		{' ', 1},
		{'中', 2},
		{'文', 2},
		{'ｗ', 2}, // fullwidth Latin
		{0, 0},
		// Emoji and combining marks are what the table used to disagree on: a
		// line with one of them put the cursor a column off.
		{'🎉', 2},
		{'́', 0}, // combining acute accent
		{'‍', 0}, // zero width joiner
		{'〈', 2}, // left-pointing angle bracket
	}
	for _, c := range cases {
		if got := runeWidth(c.r); got != c.want {
			t.Errorf("runeWidth(%q) = %d, want %d", c.r, got, c.want)
		}
	}
	if got := displayWidth([]rune("中文ab")); got != 6 {
		t.Errorf("displayWidth(中文ab) = %d, want 6", got)
	}
}

// The editor and the table must measure a character the same way, or a
// cursor move over a line ends on a different column than the line was drawn
// to. This walks the characters a person can type and holds the two together.
func TestLineEditorAndTableMeasureEveryCharacterAlike(t *testing.T) {
	for _, r := range []rune{'a', '中', 'ｗ', '🎉', '👍', '́', '‍', '️', '〈', '　', 'é'} {
		if got, want := runeWidth(r), console.RuneWidth(r); got != want {
			t.Errorf("runeWidth(%U) = %d, but the table width is %d", r, got, want)
		}
	}
}

func TestByteRuneOffsetRoundTripThroughAWideCharacter(t *testing.T) {
	s := "中a" // 3 bytes + 1 byte
	if got := runeIndexForByteOffset(s, 3); got != 1 {
		t.Errorf("runeIndexForByteOffset(%q, 3) = %d, want 1", s, got)
	}
	if got := byteOffsetForRuneIndex([]rune(s), 1); got != 3 {
		t.Errorf("byteOffsetForRuneIndex(%q, 1) = %d, want 3", s, got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
