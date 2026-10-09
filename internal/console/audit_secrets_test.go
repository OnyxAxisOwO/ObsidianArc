package console

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The audit trail is read by every administrator who holds the security
// grant, and they are not the people a code, a credential or a settings
// export was typed for. The line it records used to mask the flags marked
// Sensitive and nothing else, so everything a command takes as a positional
// argument — `setting set oauth.github_client_secret <secret>`, `2fa enable
// <code>` — was written to security_events.reason in the clear.

type auditRig struct {
	console *Console
	audit   []AuditRecord
	calls   []string
	bodies  []any
}

func newAuditRig(t *testing.T, secretSetting func(string) bool) *auditRig {
	t.Helper()
	rig := &auditRig{}
	rig.console = New(Options{
		SecretSetting: secretSetting,
		Audit:         func(_ context.Context, record AuditRecord) { rig.audit = append(rig.audit, record) },
		Dispatch: func(_ context.Context, _ user.User, method, path string, body any) (Response, error) {
			rig.calls = append(rig.calls, method+" "+path)
			rig.bodies = append(rig.bodies, body)
			return Response{Status: 200, Body: []byte(`{}`)}, nil
		},
	})
	return rig
}

func (r *auditRig) run(line string) (Result, string) {
	r.audit = nil
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
	return runLine(r.console, actor, line, false)
}

func (r *auditRig) line(t *testing.T) string {
	t.Helper()
	if len(r.audit) != 1 {
		t.Fatalf("want exactly one audit record, got %d: %+v", len(r.audit), r.audit)
	}
	return r.audit[0].Line
}

func TestAuditMasksSecretsGivenAsPositionalArguments(t *testing.T) {
	const secret = "s3cr3t-value-123456"
	rig := newAuditRig(t, func(key string) bool { return key == "acme.credential_blob" })

	cases := []struct {
		name string
		line string
		want string
	}{
		{"a core credential setting", "setting set oauth.github_client_secret " + secret, "setting set oauth.github_client_secret ***"},
		{"the turnstile secret", "setting set turnstile.secret_key " + secret, "setting set turnstile.secret_key ***"},
		{"a value of several words", "setting set oauth.oidc_client_secret " + secret + " tail", "setting set oauth.oidc_client_secret *** ***"},
		{"a credential a package declared", "setting set acme.credential_blob " + secret, "setting set acme.credential_blob ***"},
		{"a key that merely sounds like one", "setting set acme.service_api_key " + secret, "setting set acme.service_api_key ***"},
		{"the whole import document", `setting import '{"oauth.github_client_secret":"` + secret + `"}' --yes`, "setting import *** --yes"},
		{"enabling two-step sign-in", "2fa enable " + secret + " --yes", "2fa enable *** --yes"},
		{"disabling it", "2fa disable " + secret + " --yes", "2fa disable *** --yes"},
		{"replacing recovery codes", "2fa recovery " + secret + " --yes", "2fa recovery *** --yes"},
		{"unlocking the backoffice", "2fa backoffice " + secret, "2fa backoffice ***"},
		{"claiming an invite code", "me invite claim " + secret, "me invite claim ***"},
		{"an export being imported", `backup import '{"conversations":["` + secret + `"]}' --yes`, "backup import *** --yes"},
		{"a provider's custom headers", "provider create --name P --kind openai --base-url https://api.example.com --api-key " + secret + " --headers 'Authorization=Bearer " + secret + "'",
			"provider create --name=P --kind=openai --base-url=https://api.example.com --api-key=*** --headers=***"},
		{"a custom invite code", "invite create --code " + secret, "invite create --code=***"},
		{"a custom redemption code", "code create --code " + secret, "code create --code=***"},
		{"a challenge token", "credit redeem --code " + secret + " --turnstile " + secret, "credit redeem --code=*** --turnstile=***"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rig.run(c.line)
			got := rig.line(t)
			if strings.Contains(got, secret) {
				t.Errorf("the secret reached the audit line: %q", got)
			}
			if got != c.want {
				t.Errorf("audit line = %q, want %q", got, c.want)
			}
		})
	}
}

// A setting that is not a credential keeps its value in the record: what an
// administrator changed it to is the point of having a record at all.
func TestAuditKeepsTheValueOfAnOrdinarySetting(t *testing.T) {
	rig := newAuditRig(t, nil)
	rig.run("setting set site.name 'Obsidian Arc'")
	if got, want := rig.line(t), `setting set site.name "Obsidian Arc"`; got != want {
		t.Errorf("audit line = %q, want %q", got, want)
	}
}

func TestAuditMasksAnImagePassedToTheLogoCommands(t *testing.T) {
	rig := newAuditRig(t, nil)
	image := strings.Repeat("iVBORw0KGgo", 20)
	rig.run("logo set " + image)
	if got := rig.line(t); got != "logo set ***" {
		t.Errorf("audit line = %q, want the image masked", got)
	}
	rig.run("login-bg set landscape_light " + image)
	if got := rig.line(t); got != "login-bg set landscape_light ***" {
		t.Errorf("audit line = %q, want only the image masked", got)
	}
}

// The record is one row that people read, so a value cannot be allowed to end
// it early and start a row of its own, or to move the cursor of a terminal
// that prints it.
func TestAuditLineCannotBeForgedWithControlCharacters(t *testing.T) {
	rig := newAuditRig(t, nil)

	rig.run("setting set site.name \"a\nsecurity events: root disabled auditing\"")
	got := rig.line(t)
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("a line break survived into the audit line: %q", got)
	}
	if want := `setting set site.name "a\nsecurity events: root disabled auditing"`; got != want {
		t.Errorf("audit line = %q, want %q", got, want)
	}

	rig.run("setting set site.name \"\x1b[2J\x7f\u0085\u202e\"")
	got = rig.line(t)
	for _, r := range got {
		if isControl(r) || r == '\u202e' {
			t.Errorf("control character %q survived into the audit line: %q", r, got)
		}
	}
}

func TestQuoteIfNeeded(t *testing.T) {
	cases := map[string]string{
		"plain":      "plain",
		"":           `""`,
		"two words":  `"two words"`,
		"it's":       `"it's"`,
		"nbsp\u00a0": `"nbsp\u00a0"`,
		"tab\t":      `"tab\t"`,
		"esc\x1b":    `"esc\x1b"`,
		"中文":         "中文",
	}
	for in, want := range cases {
		if got := quoteIfNeeded(in); got != want {
			t.Errorf("quoteIfNeeded(%q) = %q, want %q", in, got, want)
		}
	}
}

// A masked argument is masked whether the command ran or not: the record of a
// refused command is the one most likely to hold a mistyped secret.
func TestAuditMasksTheSecretOfACommandThatFailed(t *testing.T) {
	rig := &auditRig{}
	rig.console = New(Options{
		Audit: func(_ context.Context, record AuditRecord) { rig.audit = append(rig.audit, record) },
		Dispatch: func(context.Context, user.User, string, string, any) (Response, error) {
			return Response{Status: 403, Body: []byte(`{"error":{"code":"two_factor_invalid","message":"That code is not right."}}`)}, nil
		},
	})
	result, _ := rig.run("2fa backoffice 123456")
	if result.OK {
		t.Fatal("the command was expected to fail")
	}
	if got := rig.line(t); got != "2fa backoffice ***" || rig.audit[0].OK {
		t.Errorf("audit = %+v, want the failed command with its code masked", rig.audit[0])
	}
}

// --- the image commands read no file ---

// `login-bg set` and `logo set` took anything that was not base64 as a path
// and read it with the server's own rights. On a Windows host a UNC path even
// makes the machine authenticate to wherever it points.
func TestImageCommandsNeverReadAFileNamedOnTheCommandLine(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "server-secret.png")
	if err := os.WriteFile(secretFile, []byte("not-for-the-admin-to-read"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{
		"logo set " + secretFile,
		"login-bg set landscape_light " + secretFile,
		`logo set '\\attacker\share\logo.png'`,
		"logo set /etc/passwd",
		"logo set file:///etc/passwd",
		"logo set data:image/png,notbase64",
	} {
		rig := newAuditRig(t, nil)
		result, out := rig.run(line)
		if result.OK {
			t.Errorf("%q succeeded:\n%s", line, out)
		}
		if len(rig.calls) != 0 {
			t.Errorf("%q reached the admin API with %v; nothing from the server's disk may be sent", line, rig.bodies)
		}
		if !strings.Contains(out, "base64") {
			t.Errorf("%q: the refusal does not say what is accepted:\n%s", line, out)
		}
	}
}

func TestImageCommandsAcceptBase64AndDataURLs(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		wantPath string
		wantData string
		wantMime string
	}{
		{"logo, base64", "logo set iVBORw0KGgo=", "/api/admin/logo", "iVBORw0KGgo=", "image/png"},
		{"logo, data URL", "logo set data:image/jpeg;base64,/9j/4AAQ", "/api/admin/logo", "/9j/4AAQ", "image/jpeg"},
		{"background, base64", "login-bg set portrait_dark iVBORw0KGgo=", "/api/admin/login-background/portrait_dark", "iVBORw0KGgo=", "image/png"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rig := newAuditRig(t, nil)
			result, out := rig.run(c.line)
			if !result.OK {
				t.Fatalf("refused:\n%s", out)
			}
			if len(rig.calls) != 1 || rig.calls[0] != "PUT "+c.wantPath {
				t.Fatalf("calls = %v, want PUT %s", rig.calls, c.wantPath)
			}
			body, _ := rig.bodies[0].(map[string]string)
			if body["data"] != c.wantData || body["mime"] != c.wantMime {
				t.Errorf("sent %v, want data %q mime %q", body, c.wantData, c.wantMime)
			}
		})
	}
}

// --- watch ends ---

// A watch nobody stops used to run until the connection died, and over SSH a
// connection that had died was not noticed. The transport cancels now; this
// bounds the client that stays and forgets.
func TestWatchHasADefaultAndAMaximumDuration(t *testing.T) {
	for _, c := range []struct {
		name     string
		line     string
		min, max time.Duration
	}{
		{"default", "watch --count 1 -- user list", 59 * time.Minute, time.Hour + time.Minute},
		{"explicit", "watch --count 1 --for 5m -- user list", 4 * time.Minute, 5*time.Minute + time.Minute},
		{"capped", "watch --count 1 --for 9999h -- user list", 23 * time.Hour, 24*time.Hour + time.Minute},
	} {
		t.Run(c.name, func(t *testing.T) {
			var deadline time.Time
			var has bool
			actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
			console := New(Options{Dispatch: func(ctx context.Context, _ user.User, _, _ string, _ any) (Response, error) {
				deadline, has = ctx.Deadline()
				return Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
			}})
			result, out := runLine(console, actor, c.line, false)
			if !result.OK {
				t.Fatalf("watch failed:\n%s", out)
			}
			if !has {
				t.Fatal("the watched command ran with no deadline at all")
			}
			if left := time.Until(deadline); left < c.min || left > c.max {
				t.Errorf("deadline in %v, want between %v and %v", left, c.min, c.max)
			}
		})
	}
}

func TestWatchStopsByItselfAndSaysSo(t *testing.T) {
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
	console := New(Options{Dispatch: func(context.Context, user.User, string, string, any) (Response, error) {
		return Response{Status: 200, Body: []byte(`{}`)}, nil
	}})
	var out bytes.Buffer
	session := &Session{Actor: actor, Transport: "ssh", Lang: "en", Width: 100}

	started := time.Now()
	result := console.Execute(context.Background(), session, &out, "watch --for 1s --interval 1s version")
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("watch ran for %v despite --for 1s", elapsed)
	}
	if !result.OK {
		t.Errorf("a watch that ran its course is not a failure: %+v\n%s", result, out.String())
	}
	if !strings.Contains(out.String(), "watch stopped after 1s") {
		t.Errorf("the reader was not told why it ended:\n%s", out.String())
	}
}
