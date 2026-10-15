package consolessh

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A one-shot exec closes its channel and leaves the connection open on
// purpose, so a second command can follow. Nothing then timed the connection
// out, and an account could hold its eight connection slots indefinitely with
// nothing running on them. The connection is bounded by the idle timeout once
// its last session ends.
func TestAConnectionThatRanAnExecEndsWhenIdle(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(accounts),
		IdleTimeout:  150 * time.Millisecond,
	})
	client, err := dialAs(t, srv, "admin", "s3cret-pass")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_ = session.Run("user list")
	_ = session.Close()

	if !endsWithin(client, 3*time.Second) {
		t.Fatal("a connection that finished an exec and went quiet was left open")
	}
}

// gatedListener hands out its script only once gate is closed, which lets a
// test make Accept return after Shutdown has begun. entered is closed when
// Accept is first called, so the test knows serve is really waiting in it.
type gatedListener struct {
	*scriptedListener
	gate      chan struct{}
	entered   chan struct{}
	enteredOn sync.Once
}

func (l *gatedListener) Accept() (net.Conn, error) {
	l.enteredOn.Do(func() { close(l.entered) })
	<-l.gate
	return l.scriptedListener.Accept()
}

// A connection accepted as Shutdown starts used to be admitted anyway: Shutdown
// had already taken its snapshot, so nothing closed it, and its handler sat on
// the wait group until the idle timeout. It is now closed on the spot.
func TestAConnectionAcceptedAfterShutdownBeganIsClosed(t *testing.T) {
	srv, _ := newAcceptTestServer(t)
	server, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	listener := &gatedListener{
		scriptedListener: newScriptedListener(acceptStep{conn: server}),
		gate:             make(chan struct{}),
		entered:          make(chan struct{}),
	}
	t.Cleanup(func() { _ = listener.Close() })

	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.serve(listener) }()
	select {
	case <-listener.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("serve never reached Accept")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	close(listener.gate)

	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serve returned %v, want nil after Shutdown", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not return after Shutdown")
	}
	_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("the connection accepted during shutdown was not closed: %v", err)
	}
}

// Shutdown's snapshot is taken once. A connection or session registered after
// it would be outside the snapshot, so registration refuses.
func TestRegistrationAfterShutdownIsRefused(t *testing.T) {
	srv := startTestServer(t, Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(nil),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	server, peer := net.Pipe()
	defer func() { _ = server.Close(); _ = peer.Close() }()
	if srv.addConn(server) {
		t.Fatal("a connection registered after Shutdown was admitted")
	}
	if srv.addSession(&sshSession{}) {
		t.Fatal("a session registered after Shutdown was admitted")
	}
}

// The handshake deadline reaches the socket, not a callback blocked on the
// database. A password check that never answers must still fail the sign-in
// within the handshake timeout, rather than holding a preauth slot for good.
func TestAStalledPasswordCheckEndsWithinTheHandshakeTimeout(t *testing.T) {
	previous := handshakeTimeout
	handshakeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { handshakeTimeout = previous })

	srv := startTestServer(t, Config{
		Console: newTestConsole(t),
		Authenticate: func(ctx context.Context, _, _, _ string) (user.User, string, error) {
			<-ctx.Done()
			return user.User{}, "", ctx.Err()
		},
	})

	failed := make(chan error, 1)
	go func() {
		client, err := dialAs(t, srv, "admin", "s3cret-pass")
		if err == nil {
			_ = client.Close()
		}
		failed <- err
	}()
	select {
	case err := <-failed:
		if err == nil {
			t.Fatal("a sign-in whose password check stalled was accepted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a stalled password check held the handshake past the handshake timeout")
	}
}
