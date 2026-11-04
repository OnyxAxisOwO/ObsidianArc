package consolessh

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The password check runs inside ssh.NewServerConn, so a panic there unwinds
// past the handshake. recoverHandshake catches it, and a preauth slot released
// only after the call would be skipped. With MaxUnauthenticated at one, the
// next connection would then be refused until the process restarted.
func TestAPanicDuringTheHandshakeReleasesItsSlot(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	check := fakeAuthenticate(accounts)
	var calls atomic.Int32
	srv := startTestServer(t, Config{
		Console: newTestConsole(t),
		Authenticate: func(ctx context.Context, username, password, ip string) (user.User, string, error) {
			if calls.Add(1) == 1 {
				panic("boom from the password check")
			}
			return check(ctx, username, password, ip)
		},
		MaxUnauthenticated: 1,
	})

	dial := func() (*ssh.Client, error) {
		return ssh.Dial("tcp", srv.Addr(), &ssh.ClientConfig{
			User:            "admin",
			Auth:            []ssh.AuthMethod{ssh.Password("s3cret-pass")},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         3 * time.Second,
		})
	}

	if client, err := dial(); err == nil {
		_ = client.Close()
		t.Fatal("the connection whose password check panicked was admitted")
	}
	// The slot is released before the socket is closed, so once the client has
	// seen the failure the count is already settled.
	srv.mu.Lock()
	held, addresses := srv.preauth, len(srv.preauthIP)
	srv.mu.Unlock()
	if held != 0 || addresses != 0 {
		t.Fatalf("a handshake that panicked still holds %d preauth slot(s) across %d address(es)", held, addresses)
	}

	client, err := dial()
	if err != nil {
		t.Fatalf("the next connection was refused after one panic: %v", err)
	}
	_ = client.Close()
}
