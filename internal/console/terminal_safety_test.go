package console

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A stranger's free text, read on an administrator's terminal.
//
// Anyone may file feedback, and `feedback show` printed the body and every
// reply byte for byte. Over SSH that reaches a real emulator, where an escape
// sequence can retitle the window, rewrite what is on screen or — on the
// terminals that honour them — ask the emulator to answer back; the web
// terminal acts on the clear sequences too. These tests drive the commands
// that print such text and look at the bytes that come out.

const hostileText = "hello\x1b]0;owned\x07\x1b[2J\x1b[Hworld\u009b31m\x7fgone\r\nsecond\tline"

func terminalSafetyConsole(t *testing.T, responses map[string]string) *Console {
	t.Helper()
	return New(Options{
		Dispatch: func(_ context.Context, _ user.User, method, path string, _ any) (Response, error) {
			if body, ok := responses[method+" "+path]; ok {
				return Response{Status: 200, Body: []byte(body)}, nil
			}
			return Response{Status: 404, Body: []byte(`{"error":{"code":"not_found","message":"Not found."}}`)}, nil
		},
	})
}

func marshalJSON(t *testing.T, v any) string {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func runLine(c *Console, actor user.User, line string, json bool) (Result, string) {
	var out bytes.Buffer
	session := &Session{Actor: actor, Transport: "ssh", Lang: "en", Width: 120, Colour: true, JSON: json}
	result := c.Execute(context.Background(), session, &out, line)
	return result, out.String()
}

func assertOnlyLayoutControls(t *testing.T, rendered string) {
	t.Helper()
	assertNoControlFromData(t, strings.NewReplacer("\n", "", "\t", "").Replace(rendered))
	if strings.ContainsRune(rendered, '\r') {
		t.Errorf("a carriage return survived, which lets text overwrite a line already read:\n%q", rendered)
	}
}

func TestFeedbackShownToAStaffTerminalCannotDriveIt(t *testing.T) {
	thread := marshalJSON(t, map[string]any{
		"feedback": map[string]any{"id": "f1", "kind": "bug", "priority": "low", "status": "open", "username": "mallory", "title": "t", "body": hostileText},
		"replies": []any{
			map[string]any{"id": "r1", "username": "mallory\x1b[31m", "from_staff": false, "body": hostileText},
		},
	})
	c := terminalSafetyConsole(t, map[string]string{"GET /api/admin/feedback/f1": thread})
	admin := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}

	result, out := runLine(c, admin, "feedback show f1", false)
	if !result.OK {
		t.Fatalf("feedback show failed:\n%s", out)
	}
	assertOnlyLayoutControls(t, out)
	// The harmless part of the text, and its layout, are still there.
	for _, want := range []string{"hello", "world", "gone", "second\tline", "--- mallory"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q was dropped along with the control characters:\n%q", want, out)
		}
	}
	// Dropping ESC leaves the rest of the sequence visible, so the reader
	// sees that somebody tried.
	if !strings.Contains(out, "[2J") {
		t.Errorf("the sequence was deleted silently instead of left readable:\n%q", out)
	}
}

func TestFeedbackShownToItsAuthorCannotDriveTheTerminalEither(t *testing.T) {
	thread := marshalJSON(t, map[string]any{
		"feedback": map[string]any{"id": "f1", "title": "t", "status": "open", "kind": "bug", "priority": "low", "body": hostileText},
		"replies": []any{
			map[string]any{"nickname": "Staff\x1b[2J", "from_staff": true, "body": hostileText},
		},
	})
	c := terminalSafetyConsole(t, map[string]string{"GET /api/feedback/f1": thread})
	member := user.User{ID: id.New(), Username: "ada", Role: user.RoleUser}

	result, out := runLine(c, member, "feedback thread f1", false)
	if !result.OK {
		t.Fatalf("feedback thread failed:\n%s", out)
	}
	assertOnlyLayoutControls(t, out)
	if !strings.Contains(out, "second\tline") {
		t.Errorf("the layout of the text was lost:\n%q", out)
	}
}

func TestProjectInstructionsCannotDriveTheTerminal(t *testing.T) {
	project := marshalJSON(t, map[string]any{"id": "p1", "name": "n", "instructions": hostileText})
	c := terminalSafetyConsole(t, map[string]string{"GET /api/projects/p1": project})
	member := user.User{ID: id.New(), Username: "ada", Role: user.RoleUser}

	result, out := runLine(c, member, "project show p1", false)
	if !result.OK {
		t.Fatalf("project show failed:\n%s", out)
	}
	assertOnlyLayoutControls(t, out)
	if !strings.Contains(out, "instructions:") || !strings.Contains(out, "second\tline") {
		t.Errorf("the instructions were not printed:\n%q", out)
	}
}

// The text printf-style output carries is data as well: every command that
// prints through Printf gets this without having to know.
func TestPrintfStripsControlCharactersFromWhatItFormats(t *testing.T) {
	var out bytes.Buffer
	rt := &Runtime{Out: &out}
	rt.Printf("title: %s\nbody:\t%q is quoted\n", "a\x1b[2Jb", "c\x1bd")

	assertOnlyLayoutControls(t, out.String())
	if want := "title: a[2Jb\nbody:\t\"c\\x1bd\" is quoted\n"; out.String() != want {
		t.Errorf("Printf wrote %q, want %q", out.String(), want)
	}
}

func TestSanitizeTextKeepsOnlyLineFeedAndTab(t *testing.T) {
	cases := map[string]string{
		"plain":                 "plain",
		"two\nlines\tand a tab": "two\nlines\tand a tab",
		"cr\r\nlf":              "cr\nlf",
		"esc\x1b[2Jx":           "esc[2Jx",
		"del\x7fx":              "delx",
		"c1\u009bx\u0085y":      "c1xy",
		"bell\x07nul\x00x":      "bellnulx",
		"中文 stays":              "中文 stays",
		"lone\x9bbyte":          "lone�byte",
	}
	for in, want := range cases {
		if got := sanitizeText(in); got != want {
			t.Errorf("sanitizeText(%q) = %q, want %q", in, got, want)
		}
	}
}

// --json prints what the server answered, so it cannot go through the text
// sanitiser — but encoding/json leaves DEL and the C1 range raw inside a
// string, and a terminal may act on them.
func TestRenderJSONEscapesWhatEncodingJSONLeavesRaw(t *testing.T) {
	original := map[string]any{"body": "a\x7fb\u009bc\u0085d\x1be", "ok": true}
	raw := []byte(marshalJSON(t, original))
	if !bytes.ContainsRune(raw, 0x7f) || !bytes.ContainsRune(raw, 0x9b) {
		t.Fatalf("the fixture no longer carries raw control characters: %q", raw)
	}

	var out bytes.Buffer
	if err := RenderJSON(&out, raw); err != nil {
		t.Fatal(err)
	}
	for _, r := range out.String() {
		if r != '\n' && isControl(r) {
			t.Errorf("control character %q reached the terminal in:\n%q", r, out.String())
		}
	}
	// The value itself is unchanged: it decodes to what the server sent.
	var decoded map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("the output is no longer JSON: %v\n%q", err, out.String())
	}
	if decoded["body"] != original["body"] {
		t.Errorf("body = %q, want %q", decoded["body"], original["body"])
	}
}

func TestRenderJSONFallbackIsSanitisedToo(t *testing.T) {
	var out bytes.Buffer
	if err := RenderJSON(&out, []byte("not json \x1b[2J\x9b")); err != nil {
		t.Fatal(err)
	}
	assertOnlyLayoutControls(t, out.String())
}

func TestJSONOutputOfAFeedbackThreadHasNoRawControls(t *testing.T) {
	thread := marshalJSON(t, map[string]any{
		"feedback": map[string]any{"id": "f1", "body": hostileText},
		"replies":  []any{},
	})
	c := terminalSafetyConsole(t, map[string]string{"GET /api/admin/feedback/f1": thread})
	admin := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}

	_, out := runLine(c, admin, "feedback show f1 --json", false)
	for _, r := range out {
		if r != '\n' && isControl(r) {
			t.Errorf("control character %q reached the terminal in:\n%q", r, out)
		}
	}
}

func TestAnErrorQuotingSomeonesTextCannotDriveTheTerminalEither(t *testing.T) {
	var out bytes.Buffer
	RenderError(&out, true, false, &CallError{Status: 400, Code: "bad", Message: "no such user \x1b[2Jmallory"})
	assertNoControlFromData(t, strings.ReplaceAll(out.String(), "\n", ""))
}

func TestTheBannerCannotBeDrivenByANickname(t *testing.T) {
	c := New(Options{SiteName: func() string { return "Arc\x1b[2J" }})
	banner := c.Banner(&Session{Lang: "en", Actor: user.User{Nickname: "eve\x1b]0;x\x07", Role: user.RoleUser}})
	assertOnlyLayoutControls(t, banner)
}
