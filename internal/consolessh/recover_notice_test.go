package consolessh

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A panic on the goroutine that runs the shell itself is told to the client.
// The session's own goroutine closes the connection on the way out, so the
// recovery has to run before that or its notice is written to a closed channel.
// The panic here is in the banner, which is drawn on that goroutine before any
// command, and the notice follows the session's LANG.
func TestAPanicOnTheShellGoroutineStillTellsTheClient(t *testing.T) {
	cases := map[string]struct {
		lang string
		want string
	}{
		"english": {"", "Something went wrong on our side."},
		"chinese": {"zh_CN.UTF-8", "服务器内部出错。"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			engine := console.New(console.Options{
				Dispatch: func(context.Context, user.User, string, string, any) (console.Response, error) {
					return console.Response{Status: 200, Body: []byte(`{}`)}, nil
				},
				Version:  "test",
				SiteName: func() string { panic("boom from the banner") },
			})
			accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
			srv := startTestServer(t, Config{Console: engine, Authenticate: fakeAuthenticate(accounts)})
			client := dialInsecure(t, srv, "admin", "s3cret-pass")

			session, err := client.NewSession()
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			defer session.Close()
			if tc.lang != "" {
				if err := session.Setenv("LANG", tc.lang); err != nil {
					t.Fatalf("Setenv: %v", err)
				}
			}
			if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
				t.Fatalf("RequestPty: %v", err)
			}
			stdout, err := session.StdoutPipe()
			if err != nil {
				t.Fatalf("StdoutPipe: %v", err)
			}
			out := &syncBuffer{}
			go func() { _, _ = io.Copy(out, stdout) }()
			if err := session.Shell(); err != nil {
				t.Fatalf("Shell: %v", err)
			}

			deadline := time.Now().Add(3 * time.Second)
			for !strings.Contains(out.String(), tc.want) {
				if time.Now().After(deadline) {
					t.Fatalf("the client was not told about the panic; output:\n%q", out.String())
				}
				time.Sleep(10 * time.Millisecond)
			}
			runWithin(t, 3*time.Second, "the session after the panic", func() {
				_ = session.Wait()
			})
		})
	}
}

// Every notice this package writes has a Chinese text, and it is not the English
// copied over. A missing one would show the wrong language to a Chinese session.
func TestEveryNoticeHasBothLanguages(t *testing.T) {
	notices := map[string]notice{
		"revoked":           noticeRevoked,
		"credentialChanged": noticeCredentialChanged,
		"retry":             noticeRetry,
		"internalError":     noticeInternalError,
		"typeAhead":         noticeTypeAhead,
		"idle":              noticeIdle,
		"lineTooLong":       noticeLineTooLong,
		"lineNotRun":        noticeLineNotRun,
		"commandStopped":    noticeCommandStopped,
	}
	for name, n := range notices {
		if n.en == "" || n.zh == "" {
			t.Errorf("%s: a language is empty: %+v", name, n)
		}
		if n.en == n.zh {
			t.Errorf("%s: the Chinese text is the English text", name)
		}
		if n.in("zh") != n.zh || n.in("en") != n.en || n.in("") != n.en {
			t.Errorf("%s: in() does not pick the language it was asked for", name)
		}
	}
}
