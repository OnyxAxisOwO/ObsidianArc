package consolessh

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// openPtyShell signs in and starts an interactive shell on a pty, the way an
// interactive ssh client does, and returns its input and its output.
func openPtyShell(t *testing.T, srv *Server, username, password string) (io.WriteCloser, *syncBuffer) {
	t.Helper()
	client := dialInsecure(t, srv, username, password)
	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
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
	return stdin, out
}

// liveSessions is the sessions Shutdown would close, read the way Shutdown reads them.
func liveSessions(srv *Server) []*sshSession {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	live := make([]*sshSession, 0, len(srv.sessions))
	for sess := range srv.sessions {
		live = append(live, sess)
	}
	return live
}

// abort releases the session's slots at once, so a command it leaves running
// keeps polling with nobody attached and nothing counting it.
func TestAbortCancelsAShellCommandStillRunning(t *testing.T) {
	entered := make(chan struct{})
	var enteredOnce sync.Once
	cancelled := make(chan bool, 1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	engine := console.New(console.Options{
		Dispatch: func(ctx context.Context, _ user.User, _, _ string, _ any) (console.Response, error) {
			enteredOnce.Do(func() { close(entered) })
			select {
			case <-ctx.Done():
				cancelled <- true
			case <-release:
				cancelled <- false
			}
			return console.Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
		},
		Version:  "test",
		SiteName: func() string { return "Test Arc" },
	})
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{Console: engine, Authenticate: fakeAuthenticate(accounts)})
	stdin, _ := openPtyShell(t, srv, "admin", "s3cret-pass")

	if _, err := stdin.Write([]byte("user list\r")); err != nil {
		t.Fatalf("write command: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the command never reached the admin API")
	}

	live := liveSessions(srv)
	if len(live) != 1 {
		t.Fatalf("%d live sessions, want 1", len(live))
	}
	live[0].abort()

	select {
	case wasCancelled := <-cancelled:
		if !wasCancelled {
			t.Fatal("the command was released, not cancelled")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the command kept running after its session was aborted")
	}
}

// abort also closes the connection, and the reader's failure is what ends the
// loop there, so the test above does not depend on the death channel alone.
// This one closes only the channel: the loop has to end the command on its own,
// because nothing else will report the death while the reader is blocked.
func TestSessionDeathCancelsAShellCommandWithoutTheReader(t *testing.T) {
	entered := make(chan struct{})
	var enteredOnce sync.Once
	cancelled := make(chan bool, 1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	engine := console.New(console.Options{
		Dispatch: func(ctx context.Context, _ user.User, _, _ string, _ any) (console.Response, error) {
			enteredOnce.Do(func() { close(entered) })
			select {
			case <-ctx.Done():
				cancelled <- true
			case <-release:
				cancelled <- false
			}
			return console.Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
		},
		Version:  "test",
		SiteName: func() string { return "Test Arc" },
	})
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{Console: engine, Authenticate: fakeAuthenticate(accounts)})
	stdin, _ := openPtyShell(t, srv, "admin", "s3cret-pass")

	if _, err := stdin.Write([]byte("user list\r")); err != nil {
		t.Fatalf("write command: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the command never reached the admin API")
	}

	live := liveSessions(srv)
	if len(live) != 1 {
		t.Fatalf("%d live sessions, want 1", len(live))
	}
	live[0].deadOnce.Do(func() { close(live[0].dead) })

	select {
	case wasCancelled := <-cancelled:
		if !wasCancelled {
			t.Fatal("the command was released, not cancelled")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the command kept running after its session was marked dead")
	}
}

// Shutdown waits for a shell command to stop, not only for its session. This
// command takes a moment to return once cancelled, as a query in flight does,
// and Shutdown must not report the server stopped before it has.
func TestShutdownWaitsForAShellCommandToStop(t *testing.T) {
	entered := make(chan struct{})
	var enteredOnce sync.Once
	var finished atomic.Bool

	engine := console.New(console.Options{
		Dispatch: func(ctx context.Context, _ user.User, _, _ string, _ any) (console.Response, error) {
			enteredOnce.Do(func() { close(entered) })
			<-ctx.Done()
			time.Sleep(200 * time.Millisecond)
			finished.Store(true)
			return console.Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
		},
		Version:  "test",
		SiteName: func() string { return "Test Arc" },
	})
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{Console: engine, Authenticate: fakeAuthenticate(accounts)})
	stdin, _ := openPtyShell(t, srv, "admin", "s3cret-pass")

	if _, err := stdin.Write([]byte("user list\r")); err != nil {
		t.Fatalf("write command: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the command never reached the admin API")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !finished.Load() {
		t.Fatal("Shutdown returned while the shell command was still stopping")
	}
}
