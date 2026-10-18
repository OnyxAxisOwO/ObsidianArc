package consolessh

import (
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// dialAs authenticates without the cleanup dialInsecure adds, so a test can
// watch the connection itself end.
func dialAs(t *testing.T, srv *Server, username, password string) (*ssh.Client, error) {
	t.Helper()
	return ssh.Dial("tcp", srv.Addr(), &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         3 * time.Second,
	})
}

// endsWithin reports whether the client's connection ends in time.
func endsWithin(client *ssh.Client, within time.Duration) bool {
	done := make(chan struct{})
	go func() {
		_ = client.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(within):
		return false
	}
}

// A client that signs in and never opens a session holds the connection, its
// goroutine and its descriptor for as long as it likes. Nothing timed that out:
// the idle timeout only starts once a shell does.
func TestAnAuthenticatedConnectionThatOpensNoSessionEnds(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{SessionStartTimeout: 100 * time.Millisecond, Console: newTestConsole(t), Authenticate: fakeAuthenticate(accounts)})

	client, err := dialAs(t, srv, "admin", "s3cret-pass")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if !endsWithin(client, 3*time.Second) {
		t.Fatal("a signed-in connection that never opened a session was left open")
	}
}

// A session that is opened and never asks for a shell or an exec holds one of
// the console's slots until its connection goes, so an account's four of them
// could keep every administrator out.
func TestASessionThatNeverStartsEnds(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{SessionStartTimeout: 100 * time.Millisecond, Console: newTestConsole(t), Authenticate: fakeAuthenticate(accounts)})

	client, err := dialAs(t, srv, "admin", "s3cret-pass")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := client.NewSession(); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if !endsWithin(client, 3*time.Second) {
		t.Fatal("a session that never asked for a shell or exec was left holding its slot")
	}
}

// One account may hold a handful of connections. Sessions are capped, but a
// connection that never opens one is counted by neither cap, so without this
// the number of connections an account could hold was unlimited.
func TestAnAccountCannotHoldUnlimitedConnections(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{Console: newTestConsole(t), Authenticate: fakeAuthenticate(accounts)})

	for i := 0; i < maxConnectionsPerAccount; i++ {
		client, err := dialAs(t, srv, "admin", "s3cret-pass")
		if err != nil {
			t.Fatalf("connection %d of %d: %v", i+1, maxConnectionsPerAccount, err)
		}
		t.Cleanup(func() { _ = client.Close() })
	}

	extra, err := dialAs(t, srv, "admin", "s3cret-pass")
	if err == nil {
		if !endsWithin(extra, 2*time.Second) {
			t.Fatalf("connection %d of one account was left open", maxConnectionsPerAccount+1)
		}
	}
}
