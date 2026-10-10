package consolessh

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The one-shot exec path, checked where it actually broke.
//
// `ssh host 'user list'` sends no input, so the channel reports EOF
// immediately — before the command has reached its first query. While that
// EOF was treated as an interrupt, every scripted command cancelled its own
// context on the way in and came back as an internal error from whichever
// query it happened to reach first. Nothing caught it: the package's own exec
// test ran `whoami`, which never dispatches, and accepted a non-zero exit as
// success; the server-level tests drove the web transport, which has no stdin
// to end.
//
// So this test does the one thing neither did — it watches the context the
// command was handed.

// observingConsole builds a console whose one dispatch records whether the
// context it was given was already cancelled.
func observingConsole(t *testing.T) (*console.Console, func() (called bool, cancelled bool)) {
	t.Helper()
	var (
		mu        sync.Mutex
		called    bool
		cancelled bool
	)
	dispatch := func(ctx context.Context, _ user.User, _, _ string, _ any) (console.Response, error) {
		mu.Lock()
		called = true
		cancelled = ctx.Err() != nil
		mu.Unlock()
		return console.Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
	}
	report := func() (bool, bool) {
		mu.Lock()
		defer mu.Unlock()
		return called, cancelled
	}
	return console.New(console.Options{
		Dispatch: dispatch,
		Version:  "test",
		SiteName: func() string { return "Test Arc" },
	}), report
}

func TestSSHExecDoesNotCancelTheCommandWhenTheClientSendsNoInput(t *testing.T) {
	engine, report := observingConsole(t)
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      engine,
		Authenticate: fakeAuthenticate(accounts),
	})
	client := dialInsecure(t, srv, "admin", "s3cret-pass")

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	// No Stdin is set, which is exactly what `ssh host 'user list'` does:
	// the client closes the stream straight away.
	output, runErr := session.CombinedOutput("user list")
	if runErr != nil {
		if _, ok := runErr.(*ssh.ExitError); !ok {
			t.Fatalf("run: unexpected error type %T: %v", runErr, runErr)
		}
	}

	called, cancelled := report()
	if !called {
		t.Fatalf("`user list` never reached the admin API at all; output was %q", output)
	}
	if cancelled {
		t.Error("the command was handed an already-cancelled context; " +
			"the end of the client's input is not an interrupt")
	}
	if runErr != nil {
		t.Errorf("`user list` exited non-zero over exec: %v; output %q", runErr, output)
	}
	if strings.Contains(string(output), "context canceled") {
		t.Errorf("the command reported a cancelled context:\n%s", output)
	}
}

// The other half of the same rule: Ctrl-C still has to cancel, or a long
// `watch` over SSH would be unstoppable.
func TestSSHExecStillCancelsOnCtrlC(t *testing.T) {
	ran := make(chan context.Context, 1)
	blocked := make(chan struct{})
	dispatch := func(ctx context.Context, _ user.User, _, _ string, _ any) (console.Response, error) {
		select {
		case ran <- ctx:
		default:
		}
		// Hold the command open so there is something for Ctrl-C to reach.
		select {
		case <-ctx.Done():
		case <-blocked:
		}
		return console.Response{Status: 200, Body: []byte(`{}`)}, ctx.Err()
	}
	t.Cleanup(func() { close(blocked) })

	engine := console.New(console.Options{
		Dispatch: dispatch,
		Version:  "test",
		SiteName: func() string { return "Test Arc" },
	})
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{Console: engine, Authenticate: fakeAuthenticate(accounts)})
	client := dialInsecure(t, srv, "admin", "s3cret-pass")

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	if err := session.Start("user list"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx := <-ran
	if _, err := stdin.Write([]byte{0x03}); err != nil {
		t.Fatalf("write Ctrl-C: %v", err)
	}
	<-ctx.Done()

	_ = session.Wait()
}

// A console connection outlives the moment it was authenticated.
//
// The web transport re-reads the account on every request, which is how
// disabling one stops it "now, not at its next expiry". SSH has no cookie and
// no per-request lookup, so without Reauthorize the grants an administrator
// held at the handshake were the grants they kept until they disconnected —
// revoke, demote, disable or delete had no effect on a session already open.
func TestSSHRereadsTheAccountBeforeEveryCommand(t *testing.T) {
	for _, revoked := range []struct {
		name    string
		account user.User
		err     error
	}{
		{name: "demoted to a regular account", account: regularUser("admin")},
		{name: "disabled", account: disabledAdminUser("admin")},
		{name: "deleted", err: errors.New("fake: no such account")},
	} {
		t.Run(revoked.name, func(t *testing.T) {
			engine, report := observingConsole(t)
			accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
			srv := startTestServer(t, Config{
				Console:      engine,
				Authenticate: fakeAuthenticate(accounts),
				Reauthorize: func(context.Context, string) (user.User, error) {
					return revoked.account, revoked.err
				},
			})
			client := dialInsecure(t, srv, "admin", "s3cret-pass")

			session, err := client.NewSession()
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			defer session.Close()

			output, runErr := session.CombinedOutput("user list")
			if runErr == nil {
				t.Errorf("the command succeeded after the account was revoked:\n%s", output)
			}
			if called, _ := report(); called {
				t.Error("the command reached the admin API with a revoked account")
			}
			if !strings.Contains(string(output), "no longer has console access") {
				t.Errorf("the session was not told why it ended:\n%q", output)
			}
		})
	}
}

// A watch is one command that outlives the line it was typed on, so the same
// re-read has to happen before each of its runs. Revoked in the middle, the
// next run must not reach the API, and the watch must say why and exit
// non-zero, the way a script watching for it would need.
func TestSSHWatchStopsOnItsNextRunWhenTheAccountIsRevoked(t *testing.T) {
	var dispatches atomic.Int32
	engine := console.New(console.Options{
		Dispatch: func(context.Context, user.User, string, string, any) (console.Response, error) {
			dispatches.Add(1)
			return console.Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
		},
		Version:  "test",
		SiteName: func() string { return "Test Arc" },
	})
	var reads atomic.Int32
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      engine,
		Authenticate: fakeAuthenticate(accounts),
		Reauthorize: func(context.Context, string) (user.User, error) {
			// The first read is the command starting. The second is the
			// watch's next run, by which time the account has been demoted.
			if reads.Add(1) == 1 {
				return adminUser("admin"), nil
			}
			return regularUser("admin"), nil
		},
	})
	client := dialInsecure(t, srv, "admin", "s3cret-pass")

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	output, runErr := session.CombinedOutput("watch --interval 1s --count 5 -- user list")
	if _, ok := runErr.(*ssh.ExitError); !ok {
		t.Fatalf("watch should exit non-zero once its account is revoked, got %v; output:\n%s", runErr, output)
	}
	if got := dispatches.Load(); got != 1 {
		t.Errorf("the watch reached the admin API %d times, want 1", got)
	}
	if !strings.Contains(string(output), "watch stopped") {
		t.Errorf("the session was not told why the watch ended:\n%s", output)
	}
}

// The other direction: an account that is still an administrator keeps
// working, or the re-read would have made the console unusable.
func TestSSHKeepsWorkingWhileTheAccountIsStillAnAdministrator(t *testing.T) {
	engine, report := observingConsole(t)
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      engine,
		Authenticate: fakeAuthenticate(accounts),
		Reauthorize: func(context.Context, string) (user.User, error) {
			return adminUser("admin"), nil
		},
	})
	client := dialInsecure(t, srv, "admin", "s3cret-pass")

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	if _, err := session.CombinedOutput("user list"); err != nil {
		if _, ok := err.(*ssh.ExitError); !ok {
			t.Fatalf("run: %v", err)
		}
	}
	if called, _ := report(); !called {
		t.Error("the command never reached the admin API")
	}
}

// A console connection outlives the password it was opened with. A password
// change ends the sessions a web sign-in holds, but a console connection has no
// cookie and no session row for that to reach, so the connection has to notice
// the change itself, the way it notices a revoked grant.
func TestSSHEndsItsNextCommandWhenThePasswordChanges(t *testing.T) {
	var dispatches atomic.Int32
	engine := console.New(console.Options{
		Dispatch: func(context.Context, user.User, string, string, any) (console.Response, error) {
			dispatches.Add(1)
			return console.Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
		},
		Version:  "test",
		SiteName: func() string { return "Test Arc" },
	})
	var mu sync.Mutex
	credential := "fingerprint-before"
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      engine,
		Authenticate: fakeAuthenticate(accounts),
		Reauthorize: func(context.Context, string) (user.User, error) {
			return adminUser("admin"), nil
		},
		Credential: func(context.Context, string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			return credential, nil
		},
	})
	client := dialInsecure(t, srv, "admin", "s3cret-pass")

	run := func() (string, error) {
		session, err := client.NewSession()
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		defer session.Close()
		output, err := session.CombinedOutput("user list")
		return string(output), err
	}

	if output, err := run(); err != nil {
		if _, ok := err.(*ssh.ExitError); !ok {
			t.Fatalf("the command before the change: %v; output:\n%s", err, output)
		}
	}
	if got := dispatches.Load(); got != 1 {
		t.Fatalf("the command before the change reached the admin API %d times, want 1", got)
	}

	mu.Lock()
	credential = "fingerprint-after"
	mu.Unlock()

	output, err := run()
	if _, ok := err.(*ssh.ExitError); !ok {
		t.Fatalf("a command after the password changed ran: err = %v; output:\n%s", err, output)
	}
	if got := dispatches.Load(); got != 1 {
		t.Errorf("the command after the change reached the admin API (%d dispatches in all)", got)
	}
	if !strings.Contains(output, "password for this account changed") {
		t.Errorf("the session was not told why it ended:\n%q", output)
	}
}
