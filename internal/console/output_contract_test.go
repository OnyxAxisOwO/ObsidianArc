package console

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// oneJSONDocument decodes out as exactly one JSON value. A parser reading the
// console's stdout under --json sees a second document, or text before the
// first, as a failure, so both are checked here.
func oneJSONDocument(t *testing.T, out string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		t.Fatalf("stdout holds more than one JSON document:\n%s", out)
	}
	return v
}

// user show under --json used to print the account, then the keys, because the
// keys call replaced the account body the command had just printed.
func TestUserShowUnderJSONIsOneDocument(t *testing.T) {
	aliceID := id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/users?", 200,
			`{"users":[{"id":"` + aliceID + `","username":"alice"}],"total":1}`},
		flagRoute{"GET", "/api/admin/users/" + aliceID + "/keys", 200,
			`{"keys":[{"id":"k1","prefix":"sk-a","name":"laptop"}]}`},
		flagRoute{"GET", "/api/admin/users/" + aliceID, 200,
			`{"user":{"id":"` + aliceID + `","username":"alice","role":"user","status":"active"},"cards":{},"usage":{}}`},
	)
	result, out := rig.run("user show alice --json")
	if !result.OK {
		t.Fatalf("user show alice --json failed: %s", out)
	}
	doc, ok := oneJSONDocument(t, out).(map[string]any)
	if !ok {
		t.Fatalf("the document is not an object: %s", out)
	}
	if user, _ := doc["user"].(map[string]any); user["username"] != "alice" {
		t.Errorf("the document is not the account shown: %s", out)
	}
}

// The recovery codes are printed under --json as the response itself. The
// text header that the table-mode output starts with would come before it.
func TestTwoFactorRecoveryUnderJSONIsOneDocument(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"POST", "/api/profile/two-factor/recovery", 200,
			`{"recovery_codes":["aaaa-1111","bbbb-2222"]}`},
	)
	result, out := rig.run("2fa recovery 123456 --yes --json")
	if !result.OK {
		t.Fatalf("2fa recovery --json failed: %s", out)
	}
	oneJSONDocument(t, out)
}

// security two-factor lists the administrators without two-step sign-in. Under
// --json it printed the response twice: once as the summary, once as the table.
func TestSecurityTwoFactorUnderJSONIsOneDocument(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/security/two-factor", 200,
			`{"policy":"optional","enabled":0,"accounts":2,"admins_enabled":0,"admins":1,"remember_days":30,` +
				`"admins_without":[{"id":"` + id.New() + `","username":"root","nickname":""}]}`},
	)
	result, out := rig.run("security two-factor --json")
	if !result.OK {
		t.Fatalf("security two-factor --json failed: %s", out)
	}
	oneJSONDocument(t, out)
}

// backup export prints the document it was given. Decoding it first only costs
// memory, and the decoded value was never read, so the bytes must come out as
// the server wrote them, pretty-printed, and nothing else.
func TestBackupExportPrintsTheDocumentItReceived(t *testing.T) {
	const document = `{"obsidian_arc_export":1,"conversations":[{"id":"c1","title":"Caf\u00e9 \u2713"}]}`
	rig := newFlagRig(flagRoute{"GET", "/api/account/export", 200, document})
	result, out := rig.run("backup export --json")
	if !result.OK {
		t.Fatalf("backup export --json failed: %s", out)
	}
	var want any
	if err := json.Unmarshal([]byte(document), &want); err != nil {
		t.Fatal(err)
	}
	if got := oneJSONDocument(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("export printed %v, want %v", got, want)
	}
}

// A redirect is not an export. CallRaw used to take only 4xx as a failure, so
// the body a 3xx carried was printed and the command exited OK.
func TestBackupExportRefusesARedirect(t *testing.T) {
	rig := newFlagRig(flagRoute{"GET", "/api/account/export", 302, `<a href="/login">Found</a>`})
	result, out := rig.run("backup export --json")
	if result.OK {
		t.Fatalf("a redirected export was reported as a success:\n%s", out)
	}
	if strings.Contains(out, "Found") {
		t.Errorf("the redirect's body was printed as the export:\n%s", out)
	}
}

// A WebDAV instance has no S3 endpoint or bucket. The status used to show two
// blank rows and never say which destination was in use.
func TestBackupStatusShowsAWebDAVDestination(t *testing.T) {
	rig := newFlagRig(flagRoute{"GET", "/api/admin/backup", 200,
		`{"type":"webdav","enabled":true,"configured":true,"webdav_url":"https://dav.example.test/backups",` +
			`"webdav_username":"dav-operator","endpoint":"","bucket":"","region":"us-east-1","prefix":"arc",` +
			`"interval_hours":24,"retention_hours":168}`})
	result, out := rig.run("backup status")
	if !result.OK {
		t.Fatalf("backup status failed: %s", out)
	}
	for _, want := range []string{"webdav", "https://dav.example.test/backups", "dav-operator"} {
		if !strings.Contains(out, want) {
			t.Errorf("backup status does not show %q:\n%s", want, out)
		}
	}
}

// chat list asks the server for a page. The server answers an out-of-range
// limit with its own default and no notice, so the console refuses it.
func TestChatListRefusesALimitTheServerWouldQuietlyReplace(t *testing.T) {
	rig := newFlagRig(flagRoute{"GET", "/api/conversations?", 200, `{"conversations":[]}`})

	if result, out := rig.run("chat list --limit 500"); result.OK {
		t.Fatalf("chat list --limit 500 was accepted: %s", out)
	}
	for _, call := range rig.calls {
		if strings.HasPrefix(call, "GET /api/conversations") {
			t.Fatalf("the refused limit still reached the server: %v", rig.calls)
		}
	}

	if result, out := rig.run("chat list --limit 200"); !result.OK {
		t.Fatalf("chat list --limit 200 was refused: %s", out)
	}
	if !containsCall(rig.calls, "GET /api/conversations?limit=200") {
		t.Fatalf("the limit was not sent as given: %v", rig.calls)
	}
}

// The audit line is read by everyone with the security grant. The text of a
// message or a bio is user-authored and is recorded as masked, the same as a
// password given the same way.
func TestAuditDoesNotRecordTheTextAMemberWrites(t *testing.T) {
	const secret = "my-private-message-text"
	rig := newAuditRig(t, nil)
	rig.run(fmt.Sprintf("chat edit c1 4 --content %s", secret))
	line := rig.line(t)
	if strings.Contains(line, secret) {
		t.Errorf("the audit line holds the message text: %q", line)
	}
	if !strings.Contains(line, "--content=***") {
		t.Errorf("the audit line does not mask --content: %q", line)
	}
}

// version printed its bare line under --json, which a script could not read the
// way it reads the rest of the console's output. Under --json it is one object.
// The text line stays as it was: people read it, and scripts may match it.
func TestVersionUnderJSONIsOneObjectAndTheTextLineIsUnchanged(t *testing.T) {
	const build = "v9.9.9-test"
	rig := &flagRig{bodies: map[string]any{}}
	rig.console = New(Options{Version: build})

	result, out := rig.run("version")
	if !result.OK || out != build+"\n" {
		t.Fatalf("version = %q (ok %v), want %q", out, result.OK, build+"\n")
	}

	result, out = rig.runJSON("version")
	if !result.OK {
		t.Fatalf("version --json failed: %s", out)
	}
	if got := decodeObject(t, out)["version"]; got != build {
		t.Fatalf("version --json = %s, want an object whose version is %q", out, build)
	}
}
