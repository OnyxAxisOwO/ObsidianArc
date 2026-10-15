package console

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func hardeningAdmin() user.User {
	return user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
}

// A plugin switched off between two lookups of its command. The gate answers
// yes for the first question and no for every later one, which is what the
// second lookup used to hit. The line must run the command it matched.
func TestTheCommandTheLineMatchedIsTheOneRun(t *testing.T) {
	var asks int
	gate := func(string) bool {
		asks++
		return asks == 1
	}
	c := New(Options{Plugins: gate})
	ran := false
	if err := c.AddCommands("probe", []Command{{
		Name:       "probe ping",
		Permission: Anyone,
		Run: func(context.Context, *Runtime) error {
			ran = true
			return nil
		},
	}}); err != nil {
		t.Fatal(err)
	}

	result, out := runLine(c, hardeningAdmin(), "probe ping", false)
	if !ran || !result.OK {
		t.Fatalf("ran=%v ok=%v, want the matched command to run:\n%s", ran, result.OK, out)
	}
}

// A panic in a command body must come back as an audited internal error on
// every transport. The SSH goroutine's recoverSession would only end the session
// and leave the command unaudited, so runRecovered does the recovering. The
// panic value, which can carry text the command was handed, must not reach the
// terminal.
func TestAPanickingCommandIsAnInternalErrorAndAudited(t *testing.T) {
	var audit []AuditRecord
	c := New(Options{Audit: func(_ context.Context, rec AuditRecord) { audit = append(audit, rec) }})
	if err := c.AddCommands("probe", []Command{{
		Name:       "probe boom",
		Permission: Anyone,
		Run: func(context.Context, *Runtime) error {
			panic("boom: hunter2")
		},
	}}); err != nil {
		t.Fatal(err)
	}

	result, out := runLine(c, hardeningAdmin(), "probe boom", false)
	if result.OK || result.Code != "internal" {
		t.Errorf("result = %+v, want a failed internal error", result)
	}
	if strings.Contains(out, "boom") || strings.Contains(out, "hunter2") {
		t.Errorf("the panic value reached the terminal: %q", out)
	}
	if len(audit) != 1 || audit[0].OK || audit[0].Code != "internal" || audit[0].Line != "probe boom" {
		t.Errorf("audit = %+v, want one failed record for probe boom", audit)
	}
}

// A refusal under --json is an {"error":…} object, whichever check refused the
// line, because a pipeline reading stdout expects an object and gets nothing
// else from a failing command.
func TestRefusalsUnderJSONAreEnvelopes(t *testing.T) {
	rig := newFlagRig()
	for _, line := range []string{
		"user delete alice --json",
		"nosuch --json",
		"user list --nosuchflag --json",
		// A line that does not tokenize never reaches a command, so its --json is
		// read from the raw words; a prose refusal here is the bug.
		"user list --json 'unclosed",
	} {
		result, out := runLine(rig.console, hardeningAdmin(), line, false)
		if result.OK {
			t.Errorf("%s: a refusal reported OK", line)
		}
		var envelope struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Errorf("%s: a refusal under --json is not JSON: %v\n%s", line, err, out)
			continue
		}
		if envelope.Error.Message == "" {
			t.Errorf("%s: the envelope has no message: %s", line, out)
		}
	}
}

// `user delete alice bob --yes` used to delete alice, print that, and leave
// bob alone without a word. A destructive command that does not read the extra
// positional must refuse the line, and must not echo the extra token.
func TestADestructiveCommandRefusesArgumentsItDoesNotRead(t *testing.T) {
	rig := newFlagRig()
	result, out := rig.run("user delete alice bob --yes")
	if result.OK {
		t.Fatalf("the line ran with an argument the command does not read:\n%s", out)
	}
	if !strings.Contains(out, "takes 1 argument") {
		t.Errorf("the refusal does not say why: %q", out)
	}
	if strings.Contains(out, "bob") {
		t.Errorf("the refusal echoed the extra token: %q", out)
	}
	if len(rig.calls) != 0 {
		t.Errorf("a refused line still reached the API: %v", rig.calls)
	}
}

// A non-destructive write has the same failure as a destructive one: the extra
// word is dropped and the edit goes through. `provider edit` is a PATCH, so the
// line is refused before any request, and the word is not echoed.
func TestANonDestructiveWriteRefusesArgumentsItDoesNotRead(t *testing.T) {
	rig := newFlagRig()
	result, out := rig.run("provider edit 01HZPROVIDER Other --enabled false")
	if result.OK {
		t.Fatalf("the edit ran with an argument the command does not read:\n%s", out)
	}
	if !strings.Contains(out, "takes 1 argument") {
		t.Errorf("the refusal does not say why: %q", out)
	}
	if strings.Contains(out, "Other") {
		t.Errorf("the refusal echoed the extra token: %q", out)
	}
	if len(rig.calls) != 0 {
		t.Errorf("a refused edit still reached the API: %v", rig.calls)
	}
}

// A change that names a model only partly is refused with the partial matches
// the references list gave. The models endpoint then refuses this caller, and
// that 403 used to replace the specific answer with the grant message.
func TestAModelNameRefusalIsNotReplacedByTheGrantMessage(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/references", 200, `{"models":[{"id":"01HZMODEL0000000000000000AA","display_name":"GPT-4o","model_id":"gpt-4o"}]}`},
		flagRoute{"GET", "/api/admin/models", 403, `{"error":{"code":"forbidden","message":"Forbidden."}}`},
	)
	result, out := rig.run("model order gpt")
	if result.OK {
		t.Fatalf("a partial model name was accepted for a change:\n%s", out)
	}
	if strings.Contains(out, "needs the models grant") {
		t.Errorf("the grant message replaced the partial-match refusal: %q", out)
	}
}

// backup import joins every positional, so an unquoted document with spaces in
// it is one document. The refusal must not apply to it, and the joined document
// must reach the API. The example is unquoted on purpose: the tokenizer strips
// double quotes, so a quoted-looking document would be joined into something
// that is not JSON and never get as far as the request.
func TestBackupImportReadsTheRestOfTheLine(t *testing.T) {
	rig := newFlagRig()
	_, out := rig.run(`backup import [1, 2] --yes`)
	if strings.Contains(out, "argument(s)") {
		t.Errorf("backup import refused the words of its document: %q", out)
	}
	if !slices.Contains(rig.calls, "POST /api/account/import") {
		t.Errorf("the joined document never reached the API; requests: %v\n%s", rig.calls, out)
	}
	if raw, _ := rig.bodies["POST /api/account/import"].(json.RawMessage); string(raw) != "[1, 2]" {
		t.Errorf("the document sent was %q, want [1, 2]", raw)
	}
}

// ServeMux answers an unknown path with text/plain, and a redirect has no body
// at all. Either must come back as its status, not as a decode error, and a
// redirect must not count as success.
func TestACallFailureIsReadFromItsStatusBeforeItsBody(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{404, "404 page not found\n", "request failed with status 404"},
		{301, "", "request failed with status 301"},
	} {
		rig := newFlagRig(flagRoute{"GET", "/api/admin/groups", tc.status, tc.body})
		result, out := rig.run("group list")
		if result.OK {
			t.Errorf("status %d reported OK:\n%s", tc.status, out)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("status %d: output %q, want %q", tc.status, out, tc.want)
		}
	}
}

// backup export answers through CallRaw, which has to refuse a redirect as Call
// does. A 3xx that printed as null would tell the operator the export worked.
func TestAnExportThatRedirectsIsNotReportedAsDone(t *testing.T) {
	rig := newFlagRig(flagRoute{"GET", "/api/account/export", 301, ""})
	result, out := rig.run("backup export")
	if result.OK {
		t.Errorf("a redirected export reported OK:\n%s", out)
	}
	if !strings.Contains(out, "request failed with status 301") {
		t.Errorf("output %q, want the redirect named", out)
	}
}

// The variant is a path segment, so a value with a slash in it must be escaped
// rather than read as a deeper route. Reverting to raw concatenation would send
// the request to a different path and leave every other test green.
func TestALoginBackgroundVariantIsEscapedAsOnePathSegment(t *testing.T) {
	rig := newFlagRig()
	result, _ := rig.run("login-bg clear ../x --yes")
	if !slices.Contains(rig.calls, "DELETE /api/admin/login-background/..%2Fx") {
		t.Errorf("the variant was not escaped as one segment; requests: %v", rig.calls)
	}
	if result.OK {
		t.Error("a variant the route does not know reported OK")
	}
}

func TestRegisterRefusesACommandWithNoOwner(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Register accepted a command with no Plugin, which would be core and always on")
		}
	}()
	Register(Command{
		Name:       "ownerless probe",
		Permission: Anyone,
		Run:        func(context.Context, *Runtime) error { return nil },
	})
}

// A Chinese nickname is one rune but two terminal cells. Measured in runes, the
// role column of its row starts a cell further left than on the others.
func TestRenderTableAlignsByTerminalCell(t *testing.T) {
	var out bytes.Buffer
	RenderTable(&out, 100, false, []string{"name", "role"}, [][]string{{"张三", "admin"}, {"alice", "user"}})
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got:\n%s", out.String())
	}
	cell := func(line, word string) int {
		i := strings.Index(line, word)
		if i < 0 {
			t.Fatalf("%q is not in %q", word, line)
		}
		return cellWidth(line[:i])
	}
	if a, b := cell(lines[1], "admin"), cell(lines[2], "user"); a != b {
		t.Errorf("the role column starts at cell %d on the 张三 row and %d on the alice row:\n%s", a, b, out.String())
	}
}

func TestClipToWidthKeepsWideCharactersWhole(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
		want  string
	}{
		{"张三李四", 5, "张三…"},
		{"张三李四", 4, "张…"},
		{"ab张", 2, "a…"},
		{"张", 1, ""},
		{"abc", 1, "a"},
		{"abc", 0, ""},
		{"张三", 4, "张三"},
	} {
		if got := clipToWidth(tc.in, tc.width); got != tc.want {
			t.Errorf("clipToWidth(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
		}
	}
}

// The bidi override and the tag characters are invisible or reorder text on a
// terminal. --json must spell them as escapes, and the value must still decode
// to what the server sent.
func TestRenderJSONEscapesFormatCharacters(t *testing.T) {
	original := map[string]any{"nickname": "a\u202eb\U000e0041c\u2060d"}
	raw := []byte(marshalJSON(t, original))

	var out bytes.Buffer
	if err := RenderJSON(&out, raw); err != nil {
		t.Fatal(err)
	}
	for _, r := range out.String() {
		if isFormatCharacter(r) {
			t.Errorf("format character %U reached the terminal in %q", r, out.String())
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("the output is no longer JSON: %v\n%q", err, out.String())
	}
	if decoded["nickname"] != original["nickname"] {
		t.Errorf("nickname = %q, want %q", decoded["nickname"], original["nickname"])
	}
}
