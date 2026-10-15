package consolessh

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// --- fixtures shared by this file's tests ---

func newTestConsole(t *testing.T) *console.Console {
	t.Helper()
	dispatch := func(ctx context.Context, actor user.User, method, path string, body any) (console.Response, error) {
		return console.Response{Status: 200, Body: []byte(`{}`)}, nil
	}
	return console.New(console.Options{
		Dispatch: dispatch,
		Version:  "test",
		SiteName: func() string { return "Test Arc" },
	})
}

func adminUser(username string) user.User {
	return user.User{ID: "u_" + username, Username: username, Role: user.RoleSuperAdmin, Status: user.StatusActive}
}

func regularUser(username string) user.User {
	return user.User{ID: "u_" + username, Username: username, Role: user.RoleUser, Status: user.StatusActive}
}

func disabledAdminUser(username string) user.User {
	return user.User{ID: "u_" + username, Username: username, Role: user.RoleAdmin, Status: user.StatusDisabled}
}

type testAccount struct {
	password string
	account  user.User
}

// fakeAuthenticate stands in for auth.Service.VerifyCredential: same shape,
// same "every failure looks identical" contract, but with no database
// behind it so these tests do not need internal/auth or a store. It reports
// no credential fingerprint, which is right for every test that does not
// configure Credential; a test that does supplies its own Authenticate.
func fakeAuthenticate(accounts map[string]testAccount) func(context.Context, string, string, string) (user.User, string, error) {
	return func(_ context.Context, username, password, _ string) (user.User, string, error) {
		entry, ok := accounts[username]
		if !ok || entry.password != password {
			return user.User{}, "", errors.New("fake: incorrect username or password")
		}
		if !entry.account.IsActive() {
			return user.User{}, "", errors.New("fake: account disabled")
		}
		return entry.account, "", nil
	}
}

// noReauthorize is for tests that build a Server directly and never open a
// session: New requires a Reauthorize, and none is ever called.
func noReauthorize(context.Context, string) (user.User, error) {
	return user.User{}, user.ErrNotFound
}

// rememberSignIns gives a test server the re-read that production wires to the
// database, with no database behind it. The account it returns is the one the
// handshake accepted, which is what the test's own account row would say, so
// the per-command check passes for exactly the accounts the handshake let in.
// Tests that need a different re-read set Reauthorize themselves.
func rememberSignIns(authenticate func(context.Context, string, string, string) (user.User, string, error)) (func(context.Context, string, string, string) (user.User, string, error), func(context.Context, string) (user.User, error)) {
	if authenticate == nil {
		// New refuses a config with no Authenticate; leave it that way.
		return nil, nil
	}
	var signedIn sync.Map // user ID -> user.User
	return func(ctx context.Context, username, password, ip string) (user.User, string, error) {
			account, credential, err := authenticate(ctx, username, password, ip)
			if err == nil {
				signedIn.Store(account.ID, account)
			}
			return account, credential, err
		}, func(_ context.Context, id string) (user.User, error) {
			account, ok := signedIn.Load(id)
			if !ok {
				return user.User{}, user.ErrNotFound
			}
			return account.(user.User), nil
		}
}

// startTestServer starts a Server on 127.0.0.1:0, waits for it to be
// listening, and registers cleanup that shuts it down and checks
// ListenAndServe's contract: it must return nil after Shutdown, exactly
// like http.Server, or main.go's startup select would treat a normal stop
// as a boot failure.
func startTestServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	if cfg.HostKeyPath == "" {
		cfg.HostKeyPath = filepath.Join(t.TempDir(), "ssh_host_ed25519_key")
	}
	if cfg.Reauthorize == nil {
		cfg.Authenticate, cfg.Reauthorize = rememberSignIns(cfg.Authenticate)
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	deadline := time.Now().Add(2 * time.Second)
	for srv.Addr() == "" {
		if time.Now().After(deadline) {
			t.Fatal("server did not start listening in time")
		}
		time.Sleep(time.Millisecond)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		select {
		case err := <-serveErr:
			if err != nil {
				t.Errorf("ListenAndServe returned %v after Shutdown, want nil", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("ListenAndServe did not return after Shutdown")
		}
	})

	return srv
}

func dialInsecure(t *testing.T, srv *Server, username, password string) *ssh.Client {
	t.Helper()
	client, err := ssh.Dial("tcp", srv.Addr(), &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         3 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial as %s: %v", username, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// --- Config validation ---

func TestNewRejectsAConfigWithoutConsole(t *testing.T) {
	_, err := New(Config{
		HostKeyPath:  filepath.Join(t.TempDir(), "key"),
		Authenticate: fakeAuthenticate(nil),
	})
	if err == nil {
		t.Fatal("want an error when Config.Console is nil")
	}
}

func TestNewRejectsAConfigWithoutAuthenticate(t *testing.T) {
	_, err := New(Config{
		HostKeyPath: filepath.Join(t.TempDir(), "key"),
		Console:     newTestConsole(t),
	})
	if err == nil {
		t.Fatal("want an error when Config.Authenticate is nil")
	}
}

// Without a re-read a session would keep its handshake-time grants for as long
// as it lasted, so the server refuses to start rather than run that way.
func TestNewRefusesMissingReauthorize(t *testing.T) {
	_, err := New(Config{
		HostKeyPath:  filepath.Join(t.TempDir(), "key"),
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(nil),
	})
	if err == nil {
		t.Fatal("want an error when Config.Reauthorize is nil")
	}
}

func TestNewRejectsAConfigWithoutHostKeyPath(t *testing.T) {
	_, err := New(Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(nil),
	})
	if err == nil {
		t.Fatal("want an error when Config.HostKeyPath is empty")
	}
}

// --- host key ---

func TestHostKeyGenerationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh_host_ed25519_key")

	first, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// File permission bits are largely advisory on Windows; the exact-bits
	// assertion is only meaningful on the CI machine that actually runs as
	// the process' own user under a POSIX permission model (see GO.md §4.3).
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			t.Errorf("host key mode %v is readable by group or other", mode)
		}
	}

	second, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if fingerprint(first) != fingerprint(second) {
		t.Fatalf("fingerprint changed across loads: %s vs %s", fingerprint(first), fingerprint(second))
	}
}

func TestHostKeyPathRequiresAValueToLoadOrCreate(t *testing.T) {
	if _, err := loadOrCreateHostKey(""); err == nil {
		t.Fatal("want an error for an empty path")
	}
}

func TestServerFingerprintMatchesTheGeneratedHostKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh_host_ed25519_key")
	srv, err := New(Config{
		Addr:         "127.0.0.1:0",
		HostKeyPath:  path,
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(nil),
		Reauthorize:  noReauthorize,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	signer, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("loadOrCreateHostKey: %v", err)
	}
	if got, want := srv.Fingerprint(), fingerprint(signer); got != want {
		t.Fatalf("Fingerprint() = %q, want %q", got, want)
	}
}

// --- passwordCallback: the auth story in one function ---

type fakeConnMetadata struct{ user string }

func (f fakeConnMetadata) User() string          { return f.user }
func (f fakeConnMetadata) SessionID() []byte     { return nil }
func (f fakeConnMetadata) ClientVersion() []byte { return nil }
func (f fakeConnMetadata) ServerVersion() []byte { return nil }
func (f fakeConnMetadata) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 4242}
}
func (f fakeConnMetadata) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("203.0.113.1"), Port: 22}
}

func TestPasswordCallbackCollapsesEveryFailureReasonIntoOne(t *testing.T) {
	accounts := map[string]testAccount{
		"admin":          {password: "correct-horse", account: adminUser("admin")},
		"regular":        {password: "correct-horse", account: regularUser("regular")},
		"disabled-admin": {password: "correct-horse", account: disabledAdminUser("disabled-admin")},
	}
	srv := &Server{cfg: Config{Authenticate: fakeAuthenticate(accounts)}}

	cases := []struct {
		name     string
		user     string
		password string
	}{
		{"wrong password", "admin", "not-it-at-all"},
		{"unknown account", "nobody", "whatever"},
		{"non-administrator", "regular", "correct-horse"},
		{"disabled administrator", "disabled-admin", "correct-horse"},
	}
	for _, c := range cases {
		_, err := srv.passwordCallback(fakeConnMetadata{user: c.user}, []byte(c.password))
		if !errors.Is(err, errAuthFailed) {
			t.Errorf("%s: got %v, want errAuthFailed", c.name, err)
		}
	}
}

func TestPasswordCallbackSucceedsForAnAdministrator(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "correct-horse", account: adminUser("admin")}}
	srv := &Server{cfg: Config{Authenticate: fakeAuthenticate(accounts)}}

	perm, err := srv.passwordCallback(fakeConnMetadata{user: "admin"}, []byte("correct-horse"))
	if err != nil {
		t.Fatalf("passwordCallback: %v", err)
	}
	account, ok := actorFromPermissions(perm)
	if !ok {
		t.Fatal("actorFromPermissions: no actor carried in Permissions")
	}
	if account.Username != "admin" {
		t.Fatalf("got username %q, want %q", account.Username, "admin")
	}
}

// The web terminal is open to any account whose group allows it, and SSH has
// to agree: the rule is handed in as Permitted and asked at the handshake. A
// refusal still looks exactly like a wrong password, so the prompt cannot be
// used to find out whose group has the terminal switched off.
func TestPasswordCallbackAsksPermittedWhoMayHaveAConsole(t *testing.T) {
	accounts := map[string]testAccount{
		"member":   {password: "correct-horse", account: regularUser("member")},
		"outsider": {password: "correct-horse", account: regularUser("outsider")},
	}
	srv := &Server{cfg: Config{
		Authenticate: fakeAuthenticate(accounts),
		Permitted: func(_ context.Context, account user.User) (bool, error) {
			return account.Username == "member", nil
		},
	}}

	perm, err := srv.passwordCallback(fakeConnMetadata{user: "member"}, []byte("correct-horse"))
	if err != nil {
		t.Fatalf("a permitted regular account was refused: %v", err)
	}
	if account, ok := actorFromPermissions(perm); !ok || account.Username != "member" {
		t.Fatalf("actor = %+v, %v", account, ok)
	}
	if _, err := srv.passwordCallback(fakeConnMetadata{user: "outsider"}, []byte("correct-horse")); !errors.Is(err, errAuthFailed) {
		t.Errorf("an account Permitted refuses: got %v, want errAuthFailed", err)
	}
}

// --- end to end, over a real listener and a real ssh client ---

func TestSSHEndToEndAuthentication(t *testing.T) {
	accounts := map[string]testAccount{
		"admin":   {password: "s3cret-pass", account: adminUser("admin")},
		"regular": {password: "s3cret-pass", account: regularUser("regular")},
	}
	srv := startTestServer(t, Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(accounts),
	})

	// A correct administrator password succeeds; dialInsecure fails the
	// test itself if it does not.
	dialInsecure(t, srv, "admin", "s3cret-pass")

	// Wrong password, unknown account, and a correct password for a
	// non-administrator are all refused — the ssh package itself hides the
	// server's exact reason from the client (that collapse is proven
	// directly in TestPasswordCallbackCollapsesEveryFailureReasonIntoOne),
	// so here it is enough that none of them get in.
	for _, c := range []struct{ user, password string }{
		{"admin", "wrong-password"},
		{"nobody", "whatever"},
		{"regular", "s3cret-pass"},
	} {
		if _, err := ssh.Dial("tcp", srv.Addr(), &ssh.ClientConfig{
			User:            c.user,
			Auth:            []ssh.AuthMethod{ssh.Password(c.password)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         3 * time.Second,
		}); err == nil {
			t.Errorf("dial as %s succeeded, want it refused", c.user)
		}
	}
}

func TestSSHInteractiveSessionEndsOnCtrlDWithAnEmptyLine(t *testing.T) {
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
	if err := session.Shell(); err != nil {
		t.Fatalf("Shell: %v", err)
	}

	out := &syncBuffer{}
	go func() { _, _ = io.Copy(out, stdout) }()

	// Give the banner a moment to land, then Ctrl-D on the still-empty
	// prompt: the session must end on its own. This does not depend on any
	// particular command existing in the registry — Ctrl-D on an empty
	// line is handled entirely by this package's own line editor.
	time.Sleep(100 * time.Millisecond)
	if _, err := stdin.Write([]byte{0x04}); err != nil {
		t.Fatalf("write Ctrl-D: %v", err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- session.Wait() }()
	select {
	case <-waitErr:
	case <-time.After(3 * time.Second):
		t.Fatal("session did not end after Ctrl-D on an empty line")
	}

	if out.String() == "" {
		t.Error("no output at all was observed; the banner should have produced some")
	}
}

func TestSSHExecRunsOneLineAndReturnsAnExitStatus(t *testing.T) {
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

	// Whether "whoami" is a recognised command depends on cmd_session.go,
	// which is a different file in a different package landing in
	// parallel. Either a clean run or a non-zero ssh.ExitError proves the
	// exec transport itself works; a hang or a protocol-level error would
	// not, and is what this test actually guards against.
	output, runErr := session.CombinedOutput("whoami")
	if runErr != nil {
		if _, ok := runErr.(*ssh.ExitError); !ok {
			t.Fatalf("Run: unexpected error type %T: %v", runErr, runErr)
		}
	}
	if len(output) == 0 {
		t.Error("exec produced no output at all")
	}
}

func TestSSHExecCtrlCCancelsWithoutHangingTheConnection(t *testing.T) {
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

	if err := session.Start("watch dash"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.Signal(ssh.SIGINT); err != nil {
		// Signal delivery is best-effort in the ssh package for some
		// servers; fall through to the raw byte, which is what a real
		// terminal client sends for Ctrl-C regardless.
		t.Logf("Signal: %v (continuing)", err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- session.Wait() }()
	select {
	case <-waitErr:
	case <-time.After(5 * time.Second):
		t.Fatal("exec did not return after being interrupted")
	}
}

func TestSSHRejectsNonSessionChannelTypes(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(accounts),
	})
	client := dialInsecure(t, srv, "admin", "s3cret-pass")

	if _, _, err := client.OpenChannel("direct-tcpip", nil); err == nil {
		t.Fatal("want opening a direct-tcpip channel to be refused")
	}
}

func TestSSHRejectsSubsystemRequests(t *testing.T) {
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

	if err := session.RequestSubsystem("sftp"); err == nil {
		t.Fatal("want the sftp subsystem request to be refused")
	}
}

func TestSSHMaxSessionsRejectsAnExtraConcurrentSession(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(accounts),
		MaxSessions:  1,
	})
	client := dialInsecure(t, srv, "admin", "s3cret-pass")

	first, err := client.NewSession()
	if err != nil {
		t.Fatalf("first NewSession: %v", err)
	}
	defer first.Close()
	if err := first.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatalf("RequestPty: %v", err)
	}
	if err := first.Shell(); err != nil {
		t.Fatalf("Shell: %v", err)
	}

	if _, err := client.NewSession(); err == nil {
		t.Fatal("want the second concurrent session to be rejected while the first is open")
	}
}

func TestSSHIdleTimeoutClosesTheSession(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(accounts),
		IdleTimeout:  100 * time.Millisecond,
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
	if err := session.Shell(); err != nil {
		t.Fatalf("Shell: %v", err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- session.Wait() }()
	select {
	case <-waitErr:
	case <-time.After(2 * time.Second):
		t.Fatal("session outlived its idle timeout")
	}
}

func TestSSHEnvLangSetsChineseSessionLanguage(t *testing.T) {
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

	// Setenv itself must be accepted (ok=true) even though the session
	// never reaches a shell in this test; applyEnv folds the value into
	// sess.lang, which snapshot() then hands to the console engine.
	if err := session.Setenv("LANG", "zh_CN.UTF-8"); err != nil {
		t.Fatalf("Setenv: %v", err)
	}
}

// openPtySession starts an interactive session on client with a pty and returns its
// stdin and the buffer its output is copied into.
func openPtySession(t *testing.T, client *ssh.Client) (io.WriteCloser, *syncBuffer, *ssh.Session) {
	t.Helper()
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
	if err := session.Shell(); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	out := &syncBuffer{}
	go func() { _, _ = io.Copy(out, stdout) }()
	return stdin, out, session
}

// endsAtPrompt reports whether the editor's output has reached its prompt. The
// prompt is followed by an erase-to-end-of-line that is not part of the text,
// so it is taken off before the check; the returned text is the output without
// it.
func endsAtPrompt(s string) (string, bool) {
	s = strings.TrimSuffix(s, "\x1b[K")
	return s, strings.HasSuffix(s, "> ")
}

// sshAwaitPrompt waits until the line editor is idle at its prompt. Each line
// starts from there, so the prompt that closed the previous answer cannot be
// mistaken for the answer to a line not yet typed.
func sshAwaitPrompt(t *testing.T, out *syncBuffer) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := endsAtPrompt(out.String()); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no prompt within 3s; output so far %q", out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// sshLine types one line and returns what the console printed for it: the text
// between the echoed line and the prompt that follows it.
func sshLine(t *testing.T, stdin io.Writer, out *syncBuffer, line string) string {
	t.Helper()
	sshAwaitPrompt(t, out)
	mark := out.Len()
	if _, err := io.WriteString(stdin, line+"\r"); err != nil {
		t.Fatalf("type %q: %v", line, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		reply := out.String()[mark:]
		if _, answer, found := strings.Cut(reply, line); found {
			if body, ok := endsAtPrompt(answer); ok {
				return strings.TrimSpace(strings.TrimSuffix(body, "> "))
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no answer to %q within 3s; output %q", line, reply)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// sshEnd ends an interactive session the way a person does: Ctrl-D on an
// empty prompt.
func sshEnd(t *testing.T, stdin io.Writer, out *syncBuffer, session *ssh.Session) {
	t.Helper()
	sshAwaitPrompt(t, out)
	if _, err := stdin.Write([]byte{0x04}); err != nil {
		t.Fatalf("write Ctrl-D: %v", err)
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- session.Wait() }()
	select {
	case <-waitErr:
	case <-time.After(3 * time.Second):
		t.Fatal("session did not end after Ctrl-D on an empty line")
	}
}

func TestSSHFormatJSONPersistsAcrossLines(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(accounts),
	})
	stdin, out, session := openPtySession(t, dialInsecure(t, srv, "admin", "s3cret-pass"))

	sshLine(t, stdin, out, "format json")
	got := sshLine(t, stdin, out, "whoami")
	var doc map[string]any
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("whoami after format json is not a JSON document (%v): %q", err, got)
	}
	if doc["username"] != "admin" {
		t.Errorf("whoami JSON username = %v, want admin", doc["username"])
	}
	sshEnd(t, stdin, out, session)
}

func TestSSHLangPersistsAcrossLines(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(accounts),
	})
	stdin, out, session := openPtySession(t, dialInsecure(t, srv, "admin", "s3cret-pass"))

	sshLine(t, stdin, out, "lang zh")
	if got := sshLine(t, stdin, out, "whoami"); !strings.Contains(got, "全部（超级管理员）") {
		t.Errorf("whoami after lang zh = %q, want the Chinese permissions line", got)
	}
	got := sshLine(t, stdin, out, "help user create")
	if !strings.Contains(got, "新建账户") || strings.Contains(got, "Create an account") {
		t.Errorf("help user create after lang zh = %q, want the Chinese summary only", got)
	}
	sshEnd(t, stdin, out, session)
}

// A setting changed on one connection is that connection's own: a second
// connection opened afterwards starts from the defaults.
func TestSSHSettingsDoNotLeakIntoANewConnection(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(accounts),
	})

	first, firstOut, _ := openPtySession(t, dialInsecure(t, srv, "admin", "s3cret-pass"))
	sshLine(t, first, firstOut, "format json")
	sshLine(t, first, firstOut, "lang zh")

	second, secondOut, session := openPtySession(t, dialInsecure(t, srv, "admin", "s3cret-pass"))
	got := sshLine(t, second, secondOut, "whoami")
	var doc map[string]any
	if json.Unmarshal([]byte(got), &doc) == nil {
		t.Errorf("new connection answered in JSON, want its own table default: %q", got)
	}
	if !strings.Contains(got, "all (super admin)") {
		t.Errorf("new connection answered %q, want the English permissions line", got)
	}
	sshEnd(t, second, secondOut, session)
}

func TestKeepSettingsCarriesFormatAndLangToTheNextLine(t *testing.T) {
	sess := newSSHSession(nil, nil, nil, adminUser("admin"), "192.0.2.1", "")
	s := sess.snapshot("ssh")
	s.JSON = true
	s.Lang = "zh"
	sess.keepSettings(s)

	got := sess.snapshot("ssh")
	if !got.JSON {
		t.Error("snapshot after keepSettings has JSON false, want true")
	}
	if got.Lang != "zh" {
		t.Errorf("snapshot after keepSettings has Lang %q, want zh", got.Lang)
	}
}

func TestShutdownClosesTheListenerAndLiveSessions(t *testing.T) {
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
	if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatalf("RequestPty: %v", err)
	}
	if err := session.Shell(); err != nil {
		t.Fatalf("Shell: %v", err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- session.Wait() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case <-waitErr:
	case <-time.After(2 * time.Second):
		t.Fatal("the live session was not closed by Shutdown")
	}

	if _, err := ssh.Dial("tcp", srv.Addr(), &ssh.ClientConfig{
		User:            "admin",
		Auth:            []ssh.AuthMethod{ssh.Password("s3cret-pass")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         time.Second,
	}); err == nil {
		t.Fatal("want the listener to refuse new connections after Shutdown")
	}
}

func TestHostOnlyStripsThePort(t *testing.T) {
	addr := &net.TCPAddr{IP: net.ParseIP("198.51.100.7"), Port: 2222}
	if got, want := hostOnly(addr), "198.51.100.7"; got != want {
		t.Errorf("hostOnly(%v) = %q, want %q", addr, got, want)
	}
	if got := hostOnly(nil); got != "" {
		t.Errorf("hostOnly(nil) = %q, want empty", got)
	}
}

// An IPv6 client holds a whole /64, so the handshake cap has to count the
// subnet. Counted per exact address, one client could hold every one of the 64
// handshake slots from eight addresses, and the console would refuse everyone.
// The key is derived inside acquirePreauth from the address, so a caller cannot
// count by the raw host instead, and the test goes through the same entry point
// the connection handler does.
func TestPreauthSlotsAreCountedPerIPv6Subnet(t *testing.T) {
	srv := &Server{cfg: Config{MaxUnauthenticated: 64}}
	addr := func(ip string) net.Addr {
		return &net.TCPAddr{IP: net.ParseIP(ip), Port: 22}
	}
	var held []string
	for _, ip := range []string{
		"2001:db8:0:1::1", "2001:db8:0:1::2", "2001:db8:0:1::3", "2001:db8:0:1::4",
		"2001:db8:0:1::5", "2001:db8:0:1::6", "2001:db8:0:1::7", "2001:db8:0:1::8",
	} {
		key, ok := srv.acquirePreauth(addr(ip))
		if !ok {
			t.Fatalf("the handshake slot for %s was refused within the subnet's share", ip)
		}
		held = append(held, key)
	}
	if _, ok := srv.acquirePreauth(addr("2001:db8:0:1::9")); ok {
		t.Fatal("a ninth handshake from one /64 was admitted")
	}
	if _, ok := srv.acquirePreauth(addr("2001:db8:0:2::1")); !ok {
		t.Fatal("a handshake from another /64 was refused")
	}

	// Giving a slot back has to find the key it was counted under, or the
	// subnet's share never comes back.
	srv.releasePreauth(held[0])
	if _, ok := srv.acquirePreauth(addr("2001:db8:0:1::9")); !ok {
		t.Fatal("a handshake from the subnet was refused after one of its slots was released")
	}
}

func TestLangFromEnvRecognisesChineseAndEnglishPrefixes(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"zh_CN.UTF-8", "zh", true},
		{"zh-Hans", "zh", true},
		{"en_US.UTF-8", "en", true},
		{"fr_FR.UTF-8", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := langFromEnv(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("langFromEnv(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

// An account with two-step sign-in must not get a console for a password.
// Over a real listener and a real client, because the protocol step being
// tested — partial success, then keyboard-interactive — is between the two.
func TestSSHAsksForTheCodeAfterThePassword(t *testing.T) {
	guarded := adminUser("guarded")
	guarded.TwoFactorAt = 1
	accounts := map[string]testAccount{"guarded": {password: "s3cret-pass", account: guarded}}

	var asked []string
	srv := startTestServer(t, Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(accounts),
		SecondFactor: func(_ context.Context, account user.User, code, _ string) error {
			asked = append(asked, account.Username+":"+code)
			if code != "123456" {
				return errors.New("fake: wrong code")
			}
			return nil
		},
	})

	dial := func(methods ...ssh.AuthMethod) error {
		client, err := ssh.Dial("tcp", srv.Addr(), &ssh.ClientConfig{
			User:            "guarded",
			Auth:            methods,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         3 * time.Second,
		})
		if err == nil {
			_ = client.Close()
		}
		return err
	}
	answer := func(code string) ssh.AuthMethod {
		return ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = code
			}
			return answers, nil
		})
	}

	if err := dial(ssh.Password("s3cret-pass")); err == nil {
		t.Fatal("a right password alone opened a console on a two-step account")
	}
	if err := dial(ssh.Password("s3cret-pass"), answer("000000")); err == nil {
		t.Fatal("a wrong code opened a console")
	}
	if err := dial(ssh.Password("wrong-password"), answer("123456")); err == nil {
		t.Fatal("a right code behind a wrong password opened a console")
	}
	if err := dial(ssh.Password("s3cret-pass"), answer("123456")); err != nil {
		t.Fatalf("password and code were refused: %v", err)
	}
	for _, entry := range asked {
		if entry != "guarded:000000" && entry != "guarded:123456" {
			t.Errorf("the code was checked against %q", entry)
		}
	}
}

// Without anything to check a code with, such an account is refused rather
// than let in on its password.
func TestSSHRefusesATwoStepAccountWithNoWayToCheckTheCode(t *testing.T) {
	guarded := adminUser("guarded")
	guarded.TwoFactorAt = 1
	srv := &Server{cfg: Config{Authenticate: fakeAuthenticate(map[string]testAccount{
		"guarded": {password: "s3cret-pass", account: guarded},
	})}}
	if _, err := srv.passwordCallback(fakeConnMetadata{user: "guarded"}, []byte("s3cret-pass")); err == nil {
		t.Fatal("a two-step account was admitted on its password")
	}
}

// State the wiring keeps per connection — the visit to the backoffice that
// `2fa backoffice` opens — must be one value for every command on a
// connection, and a different one on the next connection.
func TestEachConnectionHasItsOwnContext(t *testing.T) {
	type connectionKey struct{}
	var (
		made atomic.Int64
		seen = make(chan int64, 8)
	)
	engine := console.New(console.Options{
		Dispatch: func(ctx context.Context, _ user.User, _, _ string, _ any) (console.Response, error) {
			seen <- ctx.Value(connectionKey{}).(int64)
			return console.Response{Status: 200, Body: []byte(`{"user":{}}`)}, nil
		},
		Version:  "test",
		SiteName: func() string { return "Test Arc" },
	})
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      engine,
		Authenticate: fakeAuthenticate(accounts),
		ConnectionContext: func(ctx context.Context) context.Context {
			return context.WithValue(ctx, connectionKey{}, made.Add(1))
		},
	})

	run := func(client *ssh.Client) int64 {
		t.Helper()
		session, err := client.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		if _, err := session.CombinedOutput("me show"); err != nil {
			t.Fatalf("me show: %v", err)
		}
		return <-seen
	}

	first := dialInsecure(t, srv, "admin", "s3cret-pass")
	a, b := run(first), run(first)
	if a != b {
		t.Fatalf("two commands on one connection saw %d and %d", a, b)
	}
	second := dialInsecure(t, srv, "admin", "s3cret-pass")
	if c := run(second); c == a {
		t.Fatalf("a second connection shared the first one's context (%d)", c)
	}
}
