package consolessh

import (
	"context"
	"errors"
	"net"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A session that outlives its client.
//
// One-shot commands ran on a context that nothing cancelled when the channel
// went away: `serveExec` waited for the command to finish, and `watch` never
// does. MaxSessions is shared by everybody, so one account opening that many
// channels running `watch …` and killing its client held every slot, for
// everyone, until the server restarted. These tests kill the client.

func waitFor(t *testing.T, what string, within time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// openShell starts an interactive session that stays open: its stdin is a
// pipe nobody closes, because a client with no stdin sends end-of-input at
// once and the console reads that as the user leaving.
func openShell(client *ssh.Client) (*ssh.Session, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	if _, err := session.StdinPipe(); err != nil {
		return nil, err
	}
	if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		return nil, err
	}
	return session, session.Shell()
}

func TestSSHExecIsCancelledWhenTheClientHangsUp(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	stopped := make(chan struct{})
	engine := console.New(console.Options{
		Dispatch: func(ctx context.Context, _ user.User, _, _ string, _ any) (console.Response, error) {
			if calls.Add(1) > 1 {
				return console.Response{Status: 200, Body: []byte(`{}`)}, nil
			}
			close(started)
			<-ctx.Done()
			close(stopped)
			return console.Response{Status: 200, Body: []byte(`{}`)}, ctx.Err()
		},
	})
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{Console: engine, Authenticate: fakeAuthenticate(accounts), MaxSessions: 1})

	client, err := ssh.Dial("tcp", srv.Addr(), &ssh.ClientConfig{
		User: "admin", Auth: []ssh.AuthMethod{ssh.Password("s3cret-pass")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Start("user list"); err != nil {
		t.Fatal(err)
	}
	<-started

	// Not a clean close: the client is simply gone.
	_ = client.Close()

	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("the command kept running after its client hung up")
	}

	// And the one slot there is has been given back.
	next := dialInsecure(t, srv, "admin", "s3cret-pass")
	waitFor(t, "the slot to be free again", 3*time.Second, func() bool {
		session, err := next.NewSession()
		if err != nil {
			return false
		}
		defer session.Close()
		_, runErr := session.CombinedOutput("user list")
		var exit *ssh.ExitError
		return runErr == nil || errors.As(runErr, &exit)
	})
}

// The attack itself: every slot taken by `watch`, then the client killed.
func TestSSHWatchSessionsDoNotOutliveTheirClient(t *testing.T) {
	var dispatched atomic.Int32
	engine := console.New(console.Options{
		Dispatch: func(context.Context, user.User, string, string, any) (console.Response, error) {
			dispatched.Add(1)
			return console.Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
		},
	})
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console: engine, Authenticate: fakeAuthenticate(accounts),
		MaxSessions: 3, MaxSessionsPerAccount: -1,
	})

	client, err := ssh.Dial("tcp", srv.Addr(), &ssh.ClientConfig{
		User: "admin", Auth: []ssh.AuthMethod{ssh.Password("s3cret-pass")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		session, err := client.NewSession()
		if err != nil {
			t.Fatalf("session %d: %v", i, err)
		}
		if err := session.Start("watch --interval 1s -- user list"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.NewSession(); err == nil {
		t.Fatal("a fourth session was admitted past MaxSessions")
	}
	waitFor(t, "the watches to have run", 3*time.Second, func() bool { return dispatched.Load() >= 3 })

	_ = client.Close()

	// Every slot is back; a watch that ignored the hang-up would still hold
	// them (and keep dispatching).
	other := dialInsecure(t, srv, "admin", "s3cret-pass")
	opened := 0
	waitFor(t, "all three slots to be free again", 5*time.Second, func() bool {
		for opened < 3 {
			if _, err := openShell(other); err != nil {
				return false
			}
			opened++
		}
		return true
	})
}

func TestSSHExecHasAMaximumRunTime(t *testing.T) {
	engine := console.New(console.Options{
		Dispatch: func(ctx context.Context, _ user.User, _, _ string, _ any) (console.Response, error) {
			<-ctx.Done()
			return console.Response{Status: 200, Body: []byte(`{}`)}, ctx.Err()
		},
	})
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console: engine, Authenticate: fakeAuthenticate(accounts),
		ExecTimeout: 300 * time.Millisecond,
	})
	client := dialInsecure(t, srv, "admin", "s3cret-pass")
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	done := make(chan error, 1)
	var stderr strings.Builder
	session.Stderr = &stderr
	go func() { done <- session.Run("user list") }()

	select {
	case err := <-done:
		var exit *ssh.ExitError
		if !errors.As(err, &exit) || exit.ExitStatus() == 0 {
			t.Errorf("a command cut off by the clock must not look like a success: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a command that never ends was left running")
	}
	if !strings.Contains(stderr.String(), "command stopped after") {
		t.Errorf("the client was not told why: %q", stderr.String())
	}
}

func TestSSHCapsTheSessionsOneAccountCanHold(t *testing.T) {
	accounts := map[string]testAccount{
		"admin": {password: "s3cret-pass", account: adminUser("admin")},
		"other": {password: "s3cret-pass", account: adminUser("other")},
	}
	srv := startTestServer(t, Config{
		Console: newTestConsole(t), Authenticate: fakeAuthenticate(accounts),
		MaxSessions: 16, MaxSessionsPerAccount: 2,
	})

	// Two connections: the cap is on the account, not on a connection.
	first, second := dialInsecure(t, srv, "admin", "s3cret-pass"), dialInsecure(t, srv, "admin", "s3cret-pass")
	one, err := openShell(first)
	if err != nil {
		t.Fatalf("first session: %v", err)
	}
	if _, err := openShell(second); err != nil {
		t.Fatalf("second session: %v", err)
	}
	if _, err := openShell(first); err == nil {
		t.Fatal("a third session on one account was admitted")
	}
	if _, err := openShell(second); err == nil {
		t.Fatal("a third session on one account was admitted over its other connection")
	}

	// Somebody else is not locked out by it.
	if _, err := openShell(dialInsecure(t, srv, "other", "s3cret-pass")); err != nil {
		t.Fatalf("another account was refused a session: %v", err)
	}

	// And a slot comes back when a session ends. Ending one ends its whole
	// connection, so it is the other connection that asks.
	_ = one.Close()
	waitFor(t, "the account's slot to be free again", 3*time.Second, func() bool {
		_, err := openShell(second)
		return err == nil
	})
}

func TestSSHRefusesAnAbsurdUserNameBeforeCheckingAnything(t *testing.T) {
	var checked atomic.Int32
	srv := &Server{cfg: Config{Authenticate: func(context.Context, string, string, string) (user.User, error) {
		checked.Add(1)
		return user.User{}, errors.New("no")
	}}}

	_, err := srv.passwordCallback(fakeConnMetadata{user: strings.Repeat("a", maxUsernameLen+1)}, []byte("pw"))
	if !errors.Is(err, errAuthFailed) {
		t.Errorf("err = %v, want errAuthFailed", err)
	}
	if checked.Load() != 0 {
		t.Error("an over-long user name reached Authenticate")
	}

	_, _ = srv.passwordCallback(fakeConnMetadata{user: strings.Repeat("a", maxUsernameLen)}, []byte("pw"))
	if checked.Load() != 1 {
		t.Error("a name at the limit was refused before Authenticate")
	}
}

// --- connections that have not authenticated ---

func dialRaw(t *testing.T, srv *Server) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", srv.Addr(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// greeted reports whether the server sent its SSH banner, which it does the
// moment it accepts a connection into the handshake, and whether instead it
// closed the connection without a word.
func greeted(t *testing.T, conn net.Conn) bool {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if n > 0 {
		return strings.HasPrefix(string(buf[:n]), "SSH-2.0-")
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		t.Fatal("the server neither greeted nor closed the connection")
	}
	return false
}

func TestSSHCapsUnauthenticatedConnections(t *testing.T) {
	srv := startTestServer(t, Config{
		Console: newTestConsole(t), Authenticate: fakeAuthenticate(nil),
		MaxUnauthenticated: 2,
	})

	first, second := dialRaw(t, srv), dialRaw(t, srv)
	if !greeted(t, first) || !greeted(t, second) {
		t.Fatal("connections within the cap were not admitted to the handshake")
	}
	if greeted(t, dialRaw(t, srv)) {
		t.Fatal("a connection past the cap was let into the handshake")
	}

	// A slot is held only for the handshake: hanging up gives it back.
	_ = first.Close()
	waitFor(t, "a handshake slot to be free again", 3*time.Second, func() bool {
		return greeted(t, dialRaw(t, srv))
	})
}

// The allowance is for the handshake alone. A client that has authenticated
// stays connected for as long as it likes without using any of it up.
func TestSSHFinishedHandshakesGiveTheirAllowanceBack(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console: newTestConsole(t), Authenticate: fakeAuthenticate(accounts),
		MaxUnauthenticated: 1,
	})
	for i := 0; i < 3; i++ {
		dialInsecure(t, srv, "admin", "s3cret-pass")
	}
}

// The total alone would let one host fill it and keep every administrator
// out; one address gets a share of it.
func TestSSHOneAddressCannotUseUpTheUnauthenticatedAllowance(t *testing.T) {
	srv := startTestServer(t, Config{
		Console: newTestConsole(t), Authenticate: fakeAuthenticate(nil),
		MaxUnauthenticated: 64,
	})

	for i := 0; i < maxPreauthPerIP; i++ {
		if !greeted(t, dialRaw(t, srv)) {
			t.Fatalf("connection %d from one address was refused below the per-address cap", i+1)
		}
	}
	if greeted(t, dialRaw(t, srv)) {
		t.Fatal("one address held more than its share of the handshake allowance")
	}
}

// The reader that feeds an interactive session used to be left blocked on its
// send once the session had ended by itself (`exit`, the idle timeout):
// closing the connection made its next read fail, and nobody was receiving
// the failure any more. One goroutine per session, for the life of the
// process.
func TestSSHEndedShellSessionsLeaveNoGoroutinesBehind(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{Console: newTestConsole(t), Authenticate: fakeAuthenticate(accounts)})

	oneSession := func() {
		client, err := ssh.Dial("tcp", srv.Addr(), &ssh.ClientConfig{
			User: "admin", Auth: []ssh.AuthMethod{ssh.Password("s3cret-pass")},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 3 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		session, err := client.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		stdin, err := session.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
			t.Fatal(err)
		}
		if err := session.Shell(); err != nil {
			t.Fatal(err)
		}
		_, _ = stdin.Write([]byte("exit\r"))
		_ = session.Wait()
	}

	oneSession() // warm up whatever the first connection starts for good
	runtime.GC()
	before := runtime.NumGoroutine()
	const sessions = 25
	for i := 0; i < sessions; i++ {
		oneSession()
	}
	waitFor(t, "the finished sessions' goroutines to exit", 5*time.Second, func() bool {
		return runtime.NumGoroutine() <= before+sessions/5
	})
}
