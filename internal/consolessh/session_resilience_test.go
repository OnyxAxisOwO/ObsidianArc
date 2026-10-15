package consolessh

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The first Reauthorize call panics and every later one succeeds. Reauthorize
// runs on the goroutine that serves the command, so this is a panic inside a
// console goroutine with nothing above it to recover.
func panicOnceThenAdmin(t *testing.T) func(context.Context, string) (user.User, error) {
	var calls atomic.Int32
	return func(context.Context, string) (user.User, error) {
		if calls.Add(1) == 1 {
			panic("boom from a console command")
		}
		return adminUser("admin"), nil
	}
}

// runWithin fails the test if fn does not return within d. The SSH calls here
// block until the session ends, so a regression shows as a hang without it.
func runWithin(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not finish within %s", what, d)
	}
}

// A panic on the one-shot path ends that command's session, and the process
// keeps serving the next connection.
func TestAPanicInAnExecCommandEndsOnlyItsSession(t *testing.T) {
	engine, report := observingConsole(t)
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      engine,
		Authenticate: fakeAuthenticate(accounts),
		Reauthorize:  panicOnceThenAdmin(t),
	})

	first := dialInsecure(t, srv, "admin", "s3cret-pass")
	session, err := first.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	runWithin(t, 5*time.Second, "the panicking command", func() {
		_, _ = session.CombinedOutput("user list")
	})

	second := dialInsecure(t, srv, "admin", "s3cret-pass")
	next, err := second.NewSession()
	if err != nil {
		t.Fatalf("NewSession on a fresh connection: %v", err)
	}
	defer next.Close()
	if _, err := next.CombinedOutput("user list"); err != nil {
		t.Fatalf("the server stopped serving after one session panicked: %v", err)
	}
	if called, _ := report(); !called {
		t.Error("the fresh connection's command never reached the admin API")
	}
}

// The same for a command typed at the prompt, whose goroutine would otherwise
// leave the reader waiting for a result that never comes.
func TestAPanicInAnInteractiveCommandEndsOnlyItsSession(t *testing.T) {
	engine, _ := observingConsole(t)
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      engine,
		Authenticate: fakeAuthenticate(accounts),
		Reauthorize:  panicOnceThenAdmin(t),
	})

	client := dialInsecure(t, srv, "admin", "s3cret-pass")
	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatalf("RequestPty: %v", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, stdout) }()
	if err := session.Shell(); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if _, err := stdin.Write([]byte("user list\r")); err != nil {
		t.Fatalf("write command: %v", err)
	}
	runWithin(t, 5*time.Second, "the panicking shell session", func() {
		_ = session.Wait()
	})

	second := dialInsecure(t, srv, "admin", "s3cret-pass")
	next, err := second.NewSession()
	if err != nil {
		t.Fatalf("NewSession on a fresh connection: %v", err)
	}
	defer next.Close()
	if _, err := next.CombinedOutput("user list"); err != nil {
		t.Fatalf("the server stopped serving after a shell session panicked: %v", err)
	}
}

// Sixteen unread requests stop the connection's mux loop, and with it the
// keystrokes behind them. A shell that keeps answering requests must not stop
// at sixteen resizes.
func TestResizesDuringAShellDoNotStallTheTyping(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(accounts),
	})
	client := dialInsecure(t, srv, "admin", "s3cret-pass")

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatalf("RequestPty: %v", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
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

	for i := 0; i < 40; i++ {
		if err := session.WindowChange(24, 80+i); err != nil {
			t.Fatalf("WindowChange %d: %v", i, err)
		}
	}
	if _, err := stdin.Write([]byte("zq")); err != nil {
		t.Fatalf("type after the resizes: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(out.String(), "zq") {
		if time.Now().After(deadline) {
			t.Fatalf("typing stopped echoing after the resizes; output so far:\n%q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Ctrl-U first: Ctrl-D on a non-empty line deletes forward instead of
	// ending the session.
	if _, err := stdin.Write([]byte("\x15\x04")); err != nil {
		t.Fatalf("write Ctrl-U and Ctrl-D: %v", err)
	}
	runWithin(t, 3*time.Second, "the session after Ctrl-D", func() {
		_ = session.Wait()
	})
}

// A line typed while a command runs is kept and run once the command returns,
// so the lines of a multi-line paste after the first are not lost.
func TestLinesTypedDuringACommandRunAfterIt(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce atomic.Bool
	engine := console.New(console.Options{
		Dispatch: func(ctx context.Context, _ user.User, _, _ string, _ any) (console.Response, error) {
			if enteredOnce.CompareAndSwap(false, true) {
				close(entered)
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return console.Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
		},
		Version:  "test",
		SiteName: func() string { return "Test Arc" },
	})
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
	if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatalf("RequestPty: %v", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
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

	if _, err := stdin.Write([]byte("user list\r")); err != nil {
		t.Fatalf("write command: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the command never reached the admin API")
	}
	if _, err := stdin.Write([]byte("zq1\rzq2\r")); err != nil {
		t.Fatalf("type during the command: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(out.String(), "zq") {
		t.Fatalf("the editor echoed keys typed while a command was running:\n%q", out.String())
	}

	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(out.String(), "zq2") {
		if time.Now().After(deadline) {
			t.Fatalf("the line typed during the command never ran; output:\n%q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := stdin.Write([]byte{0x04}); err != nil {
		t.Fatalf("write Ctrl-D: %v", err)
	}
	runWithin(t, 3*time.Second, "the session after Ctrl-D", func() {
		_ = session.Wait()
	})
}

// A command that is still working is not an idle session, so the idle timeout
// must not cancel it. The timeout still applies once the command has returned.
func TestIdleTimeoutDoesNotCancelACommandStillRunning(t *testing.T) {
	var cancelled atomic.Bool
	release := make(chan struct{})
	engine := console.New(console.Options{
		Dispatch: func(ctx context.Context, _ user.User, _, _ string, _ any) (console.Response, error) {
			select {
			case <-release:
			case <-ctx.Done():
				cancelled.Store(true)
			}
			return console.Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
		},
		Version:  "test",
		SiteName: func() string { return "Test Arc" },
	})
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      engine,
		Authenticate: fakeAuthenticate(accounts),
		IdleTimeout:  150 * time.Millisecond,
	})
	client := dialInsecure(t, srv, "admin", "s3cret-pass")

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatalf("RequestPty: %v", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, stdout) }()
	if err := session.Shell(); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if _, err := stdin.Write([]byte("user list\r")); err != nil {
		t.Fatalf("write command: %v", err)
	}

	time.Sleep(600 * time.Millisecond)
	if cancelled.Load() {
		t.Fatal("the idle timeout cancelled a command that was still running")
	}

	close(release)
	runWithin(t, 3*time.Second, "the session after the command returned", func() {
		_ = session.Wait()
	})
}

// A lookup that fails for a reason other than the account being gone says
// nothing about the account, so the session must not claim it was revoked.
func TestATransientLookupFailureIsNotReportedAsRevocation(t *testing.T) {
	engine, report := observingConsole(t)
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      engine,
		Authenticate: fakeAuthenticate(accounts),
		Reauthorize: func(context.Context, string) (user.User, error) {
			return user.User{}, errors.New("database is locked")
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
		t.Errorf("the command ran although the account could not be checked:\n%s", output)
	}
	if called, _ := report(); called {
		t.Error("the command reached the admin API without a check")
	}
	if strings.Contains(string(output), "no longer has console access") {
		t.Errorf("a failed lookup was reported as a revocation:\n%q", output)
	}
	if !strings.Contains(string(output), "Could not verify this account") {
		t.Errorf("the session did not say the check failed:\n%q", output)
	}
	if strings.Contains(string(output), "database is locked") {
		t.Errorf("the database error reached the client:\n%q", output)
	}
}
